// SuperCli — single-binary, portable, sandboxed CLI AI agent.
//
// Usage:
//
//	supercli [--home PATH] [--provider P] [--model M] [--key K] [--base-url U]
//	         [--status] [--doctor] [--echo] [--debug]
//	         [--max-credits-per-session N] [--max-credits-per-day N]
//
// HOME RESOLUTION ORDER (highest priority first):
//  1. --home flag
//  2. $SUPERCLI_HOME
//  3. current working directory
//
// PROVIDER RESOLUTION:
//  1. CLI flags (--provider, --model, --key, --base-url)
//  2. SUPERCLI_LLM_* env vars
//  3. Default: echo provider (no API key set)
//
// The resolved home is where the .supercli/ subdirectory lives; all
// state (db, memory, sessions) is written there. Nothing is ever
// written to %APPDATA%, ~/.config or the user's home directory.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/agent"
	"supercli/internal/config"
	"supercli/internal/consult"
	"supercli/internal/credits"
	"supercli/internal/ctxexec"
	"supercli/internal/darwin"
	"supercli/internal/doctor"
	"supercli/internal/draft"
	"supercli/internal/fileops"
	"supercli/internal/freshness"
	"supercli/internal/goal"
	"supercli/internal/library"
	"supercli/internal/llm"
	"supercli/internal/memory"
	"supercli/internal/pricing"
	"supercli/internal/prompt"
	"supercli/internal/providers"
	"supercli/internal/reflect"
	"supercli/internal/session"
	"supercli/internal/shellescape"
	"supercli/internal/stats"
	"supercli/internal/storage"
	"supercli/internal/tier"
	"supercli/internal/tools"
	"supercli/internal/tui"
	"supercli/internal/ultrawork"
)

const version = "0.6.0"

// supercliSystemPromptBase is the layered system prompt
// shared by the main loop and any F6 Darwin children. It
// defaults to core + extended guidance; main() rebuilds it
// once the model's tier is known — small-tier models get the
// core only (see internal/prompt and internal/tier).
var supercliSystemPromptBase = prompt.Build(false)

// buildSystemPrompt returns the base prompt plus the
// current ISO date stamp and, if a goal service is
// passed and has an active goal, the [current_goal]
// block listing the title and pending tasks.
//
// F8: goal injection lives here so the main agent and
// any Darwin children see the same active goal.
func buildSystemPrompt(svc *goal.Service) string {
	base := supercliSystemPromptBase + "\n\n" + freshness.PromptSection(time.Now()) + "\n" + platformHint()
	if svc == nil {
		return base
	}
	injected, err := svc.Inject(context.Background(), base, 5)
	if err != nil {
		log.Printf("goal inject: %v", err)
		return base
	}
	return injected
}

// platformHint returns OS-specific shell hints so the
// model uses correct commands for the current platform.
func platformHint() string {
	switch runtime.GOOS {
	case "windows":
		return "OS: Windows. Use 'dir' instead of 'ls', 'type' instead of 'cat', 'del' instead of 'rm', 'copy' instead of 'cp', 'move' instead of 'mv', backslash paths. For shell commands use cmd /c prefix."
	case "darwin":
		return "OS: macOS. Use standard Unix commands."
	default:
		return "OS: Linux. Use standard Unix commands."
	}
}

func main() {
	// ABSOLUTE FIRST thing: catch ANY panic and log it.
	// If the program crashes silently, check .supercli/logs/crash.log.
	// `home` is captured by the closure: until --home is resolved we
	// fall back to the cwd; afterwards the crash log lands in the
	// resolved home, same as every other crash path (crash.go).
	var home string
	defer func() {
		if r := recover(); r != nil {
			h := home
			if h == "" {
				h = "."
			}
			logCrash(h, r)
			fmt.Fprintf(os.Stderr, "\nFATAL: %v\nCheck %s for stack trace.\n", r, crashLogPath(h))
			os.Exit(1)
		}
	}()

	homeFlag := flag.String("home", "", "supercli home directory (overrides $SUPERCLI_HOME and cwd)")
	showVersion := flag.Bool("version", false, "print version and exit")
	statusFlag := flag.Bool("status", false, "print session/credit usage + audit tail and exit")
	doctorFlag := flag.Bool("doctor", false, "run environment checks and exit")
	listModelsFlag := flag.Bool("list-models", false, "print known model capabilities (with --refresh, re-fetch from the provider)")
	refreshFlag := flag.Bool("refresh", false, "re-fetch the provider's /v1/models and re-probe unknowns; used with --list-models")
	modelInfoFlag := flag.String("model-info", "", "print details for a single model id and exit")
	providerFlag := flag.String("provider", "", "LLM provider: openai or echo (default: openai if SUPERCLI_LLM_API_KEY set, else echo)")
	modelFlag := flag.String("model", "", "model id (default: env SUPERCLI_LLM_MODEL, then gpt-4o-mini)")
	keyFlag := flag.String("key", "", "API key (overrides SUPERCLI_LLM_API_KEY)")
	baseFlag := flag.String("base-url", "", "base URL (overrides SUPERCLI_LLM_BASE_URL)")
	echoFlag := flag.Bool("echo", false, "force echo provider regardless of env/flags")
	debugFlag := flag.Bool("debug", false, "verbose logging")
	maxSession := flag.Int64("max-credits-per-session", 0, "cap total tokens (in+out) per session (0 = no cap)")
	maxDay := flag.Int64("max-credits-per-day", 0, "cap total tokens (in+out) per UTC day (0 = no cap)")
	draftModeFlag := flag.String("draft-mode", "critical", "F11 draft mode: off|always|balanced|critical (default critical)")
	draftModelFlag := flag.String("draft-model", "", "F11 draft model id (default: auto-pick cheapest from F16 registry)")
	configFlag := flag.String("config", "", "path to config.toml override")
	batchFlag := flag.String("batch", "", "F33: run prompt without TUI, output to stdout and exit")
	flag.Usage = usage
	flag.Parse()

	if *showVersion {
		fmt.Println("supercli", version)
		return
	}

	resolvedHome, err := storage.ResolveHome(*homeFlag)
	if err != nil {
		fatal("resolve home", err)
	}
	home = resolvedHome

	// F29: resolve config.toml hierarchy.
	// global < project < --config < env < flags.
	cwd, _ := os.Getwd()
	tomlCfg, tomlErr := config.ResolveConfig(home, cwd, *configFlag)
	if tomlErr != nil {
		log.Printf("config.toml: %v (using defaults)", tomlErr)
	}
	// Apply TOML as defaults (env/flags still win later).
	config.TomlConfigToEnv(tomlCfg)

	dataDir, err := storage.EnsureDataDir(home)
	if err != nil {
		fatal("prepare data dir", err)
	}
	// Verify write permissions — some Windows setups have
	// read-only home directories (e.g. network drives).
	if err := checkDirWritable(dataDir); err != nil {
		fatal("data dir not writable", err)
	}
	appLog := initAppLog(dataDir)
	if appLog != nil {
		defer appLog.Close()
	}

	db, err := storage.Open(home)
	if err != nil {
		fatal("open database", err)
	}
	defer db.Close()

	// F7: ensure the credit tables exist.
	creditStorage := credits.NewStorage(db)
	if err := creditStorage.Migrate(context.Background()); err != nil {
		fatal("migrate credits", err)
	}

	// F8: ensure the goal tables exist.
	goalStorage := goal.NewStorage(db)
	if err := goalStorage.Migrate(context.Background()); err != nil {
		fatal("migrate goals", err)
	}
	goalSvc := goal.NewService(goalStorage)
	if _, err := goalSvc.Refresh(context.Background()); err != nil {
		log.Printf("goal refresh: %v", err)
	}

	// --doctor short-circuits before the TUI.
	if *doctorFlag {
		// F18: build staleness report from
		// discovered skills (no provider needed).
		checker := freshness.NewChecker()
		skills := discoverSkillsForDoctor(home)
		report := checker.RunReport(nil, skills, nil)
		runDoctor(home, dataDir, creditStorage, &report)
		return
	}

	// --status short-circuits before the TUI.
	if *statusFlag {
		if err := runStatus(home, creditStorage); err != nil {
			fatal("status", err)
		}
		return
	}

	// --batch short-circuits: run prompt without TUI.
	if *batchFlag != "" {
		runBatch(*batchFlag, home, *providerFlag, *keyFlag, *baseFlag, *modelFlag, *echoFlag, *debugFlag, *draftModeFlag, *draftModelFlag)
		return
	}

	// --echo short-circuits config.
	if *echoFlag {
		*keyFlag = ""
		*providerFlag = config.ProviderEcho
	}

	cfg, err := config.Load(config.FlagOverrides{
		Provider: *providerFlag,
		APIKey:   *keyFlag,
		BaseURL:  *baseFlag,
		Model:    *modelFlag,
		Debug:    boolPtr(*debugFlag),
	})
	if err != nil {
		fatal("load config", err)
	}
	// F29: apply TOML defaults for fields not set by env/flags.
	if tomlErr == nil {
		config.ApplyTomlToConfig(&cfg, tomlCfg)
	}
	// Apply draft/credit overrides from TOML if not set by flags.
	if *draftModeFlag == "critical" && tomlCfg.DraftMode != "" {
		*draftModeFlag = tomlCfg.DraftMode
	}
	if *draftModelFlag == "" && tomlCfg.DraftModel != "" {
		*draftModelFlag = tomlCfg.DraftModel
	}
	if *maxSession == 0 && tomlCfg.MaxCreditsPerSession > 0 {
		*maxSession = tomlCfg.MaxCreditsPerSession
	}
	if *maxDay == 0 && tomlCfg.MaxCreditsPerDay > 0 {
		*maxDay = tomlCfg.MaxCreditsPerDay
	}
	if cfg.Debug {
		log.Printf("config: %+v", cfg.Sanitized())
	}

	// F16: build the capability registry from
	// seed + catalog + probe cache. A load
	// failure is logged and we fall back to an
	// empty registry — never a hardcoded table
	// (the F16 cardinal rule: the registry is
	// the only place we look up capabilities).
	caps, err := llm.NewCapabilityRegistryFromSources(home, db)
	if err != nil {
		log.Printf("capability registry: %v (using empty)", err)
		caps = llm.NewCapabilityRegistry()
	}

	// F16: --model-info short-circuits the TUI
	// so the user can inspect one model from a
	// shell. If the model is unknown, we fall
	// back to the heuristic so the user gets
	// SOME answer.
	if *modelInfoFlag != "" {
		runModelInfo(caps, *modelInfoFlag)
		return
	}

	// F16: --list-models prints the registry.
	// With --refresh, we fetch /v1/models from
	// the configured provider and register the
	// heuristic capabilities before printing.
	if *listModelsFlag {
		runListModels(caps, cfg.BaseURL, cfg.APIKey, cfg.Provider, *refreshFlag)
		return
	}

	provider, err := buildProvider(cfg, home, caps)
	if err != nil {
		fatal("init provider", err)
	}

	// Model tier (internal/tier): config glob overrides >
	// price > parsed parameter count / marker words > small.
	// Small-tier models get the core prompt only (and, below,
	// a trimmed always-on tool set) to cut per-request
	// overhead.
	tierRules := tierRulesFromToml(tomlCfg)
	var tierIn, tierOut float64
	if info, ok := caps.Get(cfg.Model); ok {
		tierIn, tierOut = info.InputCost, info.OutputCost
	}
	modelTier := tier.Classify(cfg.Model, tierIn, tierOut, tierRules)
	smallTier := modelTier == tier.Small && !tomlCfg.SmallFullTools
	supercliSystemPromptBase = prompt.Build(modelTier == tier.Small)

	registry := tools.NewRegistry()
	registry.MustRegister(tools.NewReadImage(home, 0).Spec())

	askCh := make(chan tools.AskRequest, 4)
	askUser := tools.NewAskUser(askCh)
	registry.MustRegister(askUser.Spec())

	registry.MustRegister(tools.NewSearchCode(home).Spec())

	// F21: read_zip is opt-in (not always-on).
	// The model discovers it via tool_search
	// when needed. The implementation is pure
	// stdlib (archive/zip + path/filepath), so
	// the binary stays self-contained.
	registry.MustRegister(tools.NewReadZip(home, 0).Spec())

	// F19: read_docx is opt-in (not always-on).
	// The model discovers it via tool_search
	// when needed. The implementation is pure
	// stdlib (archive/zip + encoding/xml); no
	// libreoffice, no docx library, no shell-out.
	registry.MustRegister(tools.NewReadDocx(home, 0).Spec())

	// F22: read_xlsx is opt-in (not always-on).
	// The model discovers it via tool_search
	// when needed. The implementation is pure
	// stdlib (archive/zip + encoding/xml); no
	// excelize, no libreoffice, no shell-out.
	// Renders cells as a markdown-style
	// pipe-separated table (one row per line,
	// cells joined by " | ").
	registry.MustRegister(tools.NewReadXlsx(home, 0).Spec())

	// Wave-2 office editing: edit_docx / edit_xlsx
	// rewrite a single zip entry byte-for-byte safe
	// (temp file + atomic swap + .bak backup), pure
	// stdlib. file_ops is the safe file manager for
	// office users: no overwrite, no hard delete
	// (trash folder instead), sandboxed paths.
	registry.MustRegister(tools.NewEditDocx(home).Spec())
	registry.MustRegister(tools.NewEditXlsx(home).Spec())
	registry.MustRegister(tools.NewFileOps(home).Spec())

	// F20: read_pdf is opt-in (not always-on).
	// The model discovers it via tool_search
	// when needed. The implementation uses
	// github.com/ledongthuc/pdf (pure Go, no
	// cgo, no shell-out — no pdftotext, no
	// poppler). Pages are separated by
	// "--- Page N ---" headers.
	registry.MustRegister(tools.NewReadPdf(home, 0).Spec())

	// F23: send_screenshot is opt-in (not
	// always-on). The model discovers it via
	// tool_search when it needs to attach a
	// clipboard image to the next message.
	// The capture uses OS-specific commands
	// (PowerShell / osascript / xclip /
	// wl-paste) and the F16 vision gate
	// refuses the call if the current model
	// doesn't support image input.
	registry.MustRegister(tools.NewSendScreenshot(home, caps.HasVision).Spec())

	ftsIndex, err := tools.NewInMemoryIndex()
	if err != nil {
		fatal("init fts index", err)
	}
	defer ftsIndex.Close()
	toolSearcher := tools.NewToolSearcher(registry, ftsIndex)
	registry.MustRegister(toolSearcher.Spec())

	discoverer := tools.NewDiscoverer(home, home)
	skillApplier := tools.NewSkillApplier(discoverer)
	registry.MustRegister(skillApplier.Spec())

	userLoader := tools.NewUserToolLoader(home, home)
	userTools, userErrs := userLoader.Load()
	for _, e := range userErrs {
		log.Printf("user tool load: %v", e)
	}
	for _, t := range userTools {
		registry.MustRegister(t)
	}

	if err := toolSearcher.RebuildIndex(); err != nil {
		log.Printf("rebuild fts index: %v", err)
	}

	logsDir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		log.Printf("mkdir logs: %v", err)
	}
	errorLog, err := tools.NewErrorLog(filepath.Join(logsDir, "tool_errors.log"))
	if err != nil {
		log.Printf("open error log: %v", err)
	}
	defer errorLog.Close()

	// F7: open the audit log. A failure here is
	// non-fatal; we just skip audit (status bar will
	// show "audit: off").
	audit, auditErr := credits.NewAudit(home)
	if auditErr != nil {
		log.Printf("audit log: %v", auditErr)
		audit = nil
	} else {
		defer audit.Close()
	}

	// Tier-aware always-on tools. Small-tier models get a
	// trimmed core set (file read/edit, office editing,
	// goal/memory, tool_search) so their tiny context isn't
	// burned on tool schemas; everything else stays reachable
	// via tool_search. `small_full_tools = true` in
	// config.toml restores the full set.
	if smallTier {
		for _, name := range []string{
			"read_lines", "read_context", "edit_line",
			"insert_after", "delete_lines",
			"file_ops", "edit_docx", "edit_xlsx",
			"goal", "tool_search",
		} {
			registry.MarkAlwaysOn(name)
		}
	} else {
		registry.MarkAlwaysOn("read_image")
		registry.MarkAlwaysOn("ask_user")
		registry.MarkAlwaysOn("tool_search")
		registry.MarkAlwaysOn("apply_skill")
		registry.MarkAlwaysOn("darwin")
		registry.MarkAlwaysOn("goal")
		registry.MarkAlwaysOn("ctx_execute")
	}

	// F10: ctx_execute is the context-mode sandbox.
	// The model uses it instead of file_read for
	// large files: writes a small script, gets
	// bounded stdout back. Always-on so it sees the
	// token-savings tool from turn 1.
	ctxRunner := ctxexec.New(home)
	ctxTool := tools.NewCtxExecuteTool(ctxRunner, home)
	registry.MustRegister(ctxTool.Spec())

	// F8: goal tool is always-on. It exposes the active
	// goal and tasks to the model. Decomposition uses
	// the main provider when available, else heuristic.
	goalTool := tools.NewGoalTool(goalSvc)
	goalTool.SetDecompose(goalProviderAdapter{Provider: provider}, provider.Name())
	registry.MustRegister(goalTool.Spec())

	darwinTool := darwin.NewDarwinTool(provider, registry, home, buildSystemPrompt(goalSvc))
	// Each Darwin child gets its own file-tool registry
	// rooted at its git worktree, so its edits land on
	// its branch (and can be diffed, judged, and merged)
	// instead of in the parent's cwd.
	darwinTool.SetLoopFactory(darwin.AgentLoopAdapter(registry, func(root string) (*tools.Registry, error) {
		return buildChildToolRegistry(root), nil
	}))
	registry.MustRegister(darwinTool.Spec())

	memStore, memErr := memory.OpenStore(home)
	if memErr != nil {
		log.Printf("memory store: %v (F5 disabled)", memErr)
		memStore = nil
	} else {
		defer memStore.Close()
	}

	// Persistent memory tools: always-on so the model can
	// save and recall facts across sessions (the system
	// prompt's Memory section tells it when). Skipped when
	// the store failed to open.
	if memStore != nil {
		registry.MustRegister(tools.NewRemember(memStore).Spec())
		registry.MustRegister(tools.NewRecall(memStore).Spec())
		registry.MarkAlwaysOn("remember")
		registry.MarkAlwaysOn("recall")
	}

	var injector *reflect.Injector
	if memStore != nil {
		patStore, _ := reflect.NewStore(memStore)
		if patStore != nil {
			ext := &reflect.Extractor{
				ErrorsPath:  filepath.Join(logsDir, "tool_errors.log"),
				MaxPatterns: 5,
			}
			patterns, extErr := ext.Extract(context.Background())
			if extErr != nil {
				log.Printf("F5 extract: %v", extErr)
			} else if len(patterns) > 0 {
				if saveErr := patStore.SaveAll(context.Background(), patterns); saveErr != nil {
					log.Printf("F5 save: %v", saveErr)
				} else {
					log.Printf("F5: stored %d patterns", len(patterns))
				}
			}
			injector = &reflect.Injector{Store: patStore}
		}
	}

	// F7: build the credit tracker. The session id is
	// the cwd-based default; for F7 we do not yet
	// support --resume so each launch is a new
	// session.
	budget := credits.Budget{PerSession: *maxSession, PerDay: *maxDay}
	if err := budget.Validate(); err != nil {
		fatal("credit budget", err)
	}
	sessionID := fmt.Sprintf("sess-%d", time.Now().UnixNano())
	tracker := credits.NewTracker(sessionID, budget, creditStorage)
	if err := creditStorage.SaveBudget(context.Background(), sessionID, budget); err != nil {
		log.Printf("save budget: %v", err)
	}
	defer tracker.Close()

	// F28: fetch external prices (pricepertoken.com, OpenRouter)
	// and push fetched rates into the credits package so
	// CostFor/StatusBar use live prices. Non-fatal: if all
	// sources fail, the hardcoded fallback rates in
	// credits/cost.go still work. Cache is saved to disk.
	fetcher := pricing.NewFetcher(home)
	fetcher.FetchAndUpdate(caps.All())

	// F13: open the session store. Messages get persisted
	// as the loop emits them, and a FTS5 index on
	// messages.content keeps the search_history tool fast.
	// A failure here is non-fatal: search_history is
	// disabled, but the loop still runs in-memory.
	sessStore, sessErr := session.OpenStore(home)
	if sessErr != nil {
		log.Printf("session store: %v (search_history disabled)", sessErr)
		sessStore = nil
	} else {
		defer sessStore.Close()
	}
	var sessWriter agent.SessionWriter
	if sessStore != nil {
		sessWriter = session.NewWriter(sessStore, sessionID)
		// Opt-in tool: the model discovers it via
		// tool_search when it wants to recall prior
		// sessions. We do NOT MarkAlwaysOn.
		registry.MustRegister(tools.NewSearchHistory(sessStore).Spec())
	}

	// F11 draft wiring. Build the draft policy +
	// provider + sink + stats only when F11 is not
	// explicitly off. The draft model is picked
	// from the F16 CapabilityRegistry's
	// SuggestCheapestForTask("plan") — never
	// hardcoded in Go (D1 decision).
	draftPolicy, draftProvider := buildDraftWiring(*draftModeFlag, *draftModelFlag, provider, caps, cfg, tierRules)
	var draftSink agent.DraftOverrideSink
	if draftProvider != nil {
		draftSink = reflect.NewJSONLDraftOverrideSink(filepath.Join(home, ".supercli", "reflect"))
	}
	draftStats := stats.NewMemory()

	// F5.a: mid-run reflection checkpoint. Every
	// reflectEvery steps the loop asks the model for a
	// 2-3 sentence self-review and injects it as a
	// system message. Default 8; config.toml
	// reflect_every overrides (negative disables).
	reflectEvery := 8
	if tomlCfg.ReflectEvery != 0 {
		reflectEvery = tomlCfg.ReflectEvery
	}
	reflector := &reflect.ModelReflector{Provider: provider}

	// Build the real loop. Pass the home as the image base dir.
	loop, err := agent.NewLoop(agent.LoopConfig{
		Provider:        provider,
		Registry:        registry,
		System:          buildSystemPrompt(goalSvc),
		MaxSteps:        10,
		ErrorLog:        errorLog,
		Reflector:       reflector,
		ReflectEvery:    reflectEvery,
		PatternInjector: injector,
		CreditTracker:   tracker,
		Writer:          sessWriter,
		Ultrawork: &ultrawork.Wiring{
			Goal:        ultraworkGoalAdapter{svc: goalSvc},
			Credit:      ultraworkCreditAdapter{tracker: tracker},
			SisyphusMax: 3,
		},
		Draft:             draftPolicy,
		DraftProvider:     draftProvider,
		DraftOverrideSink: draftSink,
		Stats:             draftStats,
	})
	if err != nil {
		fatal("init agent", err)
	}

	subReg := agent.NewSubAgentRegistry()
	agent.MustRegisterAll(subReg, agent.BuiltinSubAgents())
	at, err := agent.NewAgentTool(
		subReg,
		loop,
		registry,
		provider,
		caps,
		func(cfg agent.LoopConfig) (*agent.Loop, error) {
			return agent.NewLoop(cfg)
		},
	)
	if err != nil {
		fatal("init agent tool", err)
	}
	registry.MustRegister(at.Spec())

	// F14: opt-in tool. The model calls hide_messages
	// when it wants to drop old messages from its own
	// context. We bind it after NewLoop because the tool
	// needs the loop as Hider and a way to ask its
	// current Messages length.
	hideTool := tools.NewHideMessages(loop, func() int {
		return len(loop.AllMessages())
	})
	registry.MustRegister(hideTool.Spec())

	// F14: /clear slash command. Hides all but the last
	// 2 user turns from the model's view. Cheaper than
	// "new session" because the TUI scrollback, the
	// FTS5 search index, and the on-disk session.db
	// all stay intact.
	mergedCommands := mergedSlashCommands(darwinTool, goalSvc)
	mergedCommands["clear"] = func(ctx context.Context, args string) (string, error) {
		hidden := loop.HideLastUserTurns(2)
		if hidden == 0 {
			return "nothing to clear", nil
		}
		return fmt.Sprintf("cleared: hid %d message(s) from context (scrollback kept)", hidden), nil
	}

	// F12: cross-model consultation. Build the
	// council once, share it between the opt-in
	// `consult` tool and the `/council` slash
	// command. The OnResult callback emits a
	// ConsultEvent for the TUI transcript marker.
	council := buildConsultCouncil(3, provider, caps, cfg)
	if council != nil {
		consultTool := tools.NewConsult(council)
		consultTool.OnResult = func(r consult.Result) {
			winner := -1
			prov := ""
			if !r.AllFailed && r.Verdict.WinnerIndex >= 0 &&
				r.Verdict.WinnerIndex < len(r.Candidates) {
				winner = r.Verdict.WinnerIndex
				prov = r.Candidates[winner].Provider
			}
			loop.Emit(agent.ConsultEvent{
				Question:       r.Question,
				CandidateCount: len(r.Candidates),
				WinnerIndex:    winner,
				WinnerProvider: prov,
				Reason:         r.Verdict.Reason,
				AllFailed:      r.AllFailed,
				TotalTokens:    r.TotalTokens,
			})
		}
		registry.MustRegister(consultTool.Spec())
	}
	mergedCommands["council"] = func(ctx context.Context, args string) (string, error) {
		if council == nil {
			return "council: not wired (no models available)", nil
		}
		q := strings.TrimSpace(args)
		if q == "" {
			return "usage: /council [N] <prompt>  (N defaults to 3)", nil
		}
		n := 3
		// Parse optional leading N.
		if fields := strings.Fields(q); len(fields) > 1 {
			if v, err := strconv.Atoi(fields[0]); err == nil && v > 0 {
				n = v
				q = strings.TrimSpace(strings.TrimPrefix(q, fields[0]))
			}
		}
		res, err := council.Consult(ctx, consult.Request{Question: q, N: n})
		if err != nil {
			return fmt.Sprintf("council: %v", err), nil
		}
		if res.AllFailed {
			return "council: all samples failed", nil
		}
		w := res.Candidates[res.Verdict.WinnerIndex]
		return fmt.Sprintf("winner (#%d, %s):\n%s\n\njudge: %s\n[%d candidate(s), %d tokens]",
			res.Verdict.WinnerIndex+1, w.Provider, w.Response, res.Verdict.Reason,
			len(res.Candidates), res.TotalTokens), nil
	}

	// F25a: /help — list all registered slash commands.
	mergedCommands["help"] = func(ctx context.Context, args string) (string, error) {
		return tui.HelpContent(), nil
	}

	// F25a: /reflect — show learned patterns from reflection.
	mergedCommands["reflect"] = func(ctx context.Context, args string) (string, error) {
		if injector == nil {
			return "reflect: no patterns learned yet (F5 memory store not available)", nil
		}
		suffix, err := injector.Build(ctx, "")
		if err != nil {
			return fmt.Sprintf("reflect: %v", err), nil
		}
		if suffix == "" {
			return "reflect: no relevant patterns found", nil
		}
		return suffix, nil
	}

	// F25a: /compact — real context compaction. The active
	// model summarizes the conversation (9-section prompt),
	// then every non-system message is replaced by a single
	// system message containing the summary plus a resume
	// wrapper. The dropped messages stay in the F13 session
	// store and remain searchable via search_history.
	mergedCommands["compact"] = func(ctx context.Context, args string) (string, error) {
		msgs := loop.AllMessages()
		nonSystem := 0
		for _, m := range msgs {
			if m.Role != llm.RoleSystem {
				nonSystem++
			}
		}
		if nonSystem == 0 {
			return "compact: nothing to compact (already minimal)", nil
		}
		summary, err := summarizeForCompaction(ctx, loop.Provider(), msgs)
		if err != nil {
			return fmt.Sprintf("compact: summarization failed: %v (context unchanged)", err), nil
		}
		removed := loop.CompactWithSummary(wrapCompactSummary(summary))
		return fmt.Sprintf("compact: replaced %d message(s) with a %d-char summary", removed, len(summary)), nil
	}

	// F25a: /status — show credits and session info.
	mergedCommands["status"] = func(ctx context.Context, args string) (string, error) {
		sessUsed, dayUsed := tracker.Used()
		budget := tracker.Budget()
		name := provider.Name()
		var b strings.Builder
		fmt.Fprintf(&b, "model: %s\n", name)
		if budget.PerSession > 0 {
			fmt.Fprintf(&b, "session: %d / %d tokens (%.0f%%)\n",
				sessUsed, budget.PerSession, float64(sessUsed)/float64(budget.PerSession)*100)
		} else {
			fmt.Fprintf(&b, "session: %d tokens (no cap)\n", sessUsed)
		}
		if budget.PerDay > 0 {
			fmt.Fprintf(&b, "daily: %d / %d tokens (%.0f%%)\n",
				dayUsed, budget.PerDay, float64(dayUsed)/float64(budget.PerDay)*100)
		} else {
			fmt.Fprintf(&b, "daily: %d tokens (no cap)\n", dayUsed)
		}
		return b.String(), nil
	}

	// F25a: /models — list available models.
	mergedCommands["models"] = func(ctx context.Context, args string) (string, error) {
		models := caps.All()
		if len(models) == 0 {
			return "models: no models registered (run --list-models --refresh to populate)", nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d model(s) registered:\n", len(models))
		for _, m := range models {
			flags := ""
			if m.Vision {
				flags += "V"
			}
			if m.ToolUse {
				flags += "T"
			}
			if m.Stream {
				flags += "S"
			}
			if flags != "" {
				flags = " [" + flags + "]"
			}
			fmt.Fprintf(&b, "  %s%s\n", m.ID, flags)
		}
		return b.String(), nil
	}

	// F25a: /sandbox — show sandbox status.
	mergedCommands["sandbox"] = func(ctx context.Context, args string) (string, error) {
		return fmt.Sprintf("sandbox: active\nhome: %s\ndata: %s", home, dataDir), nil
	}

	// F17: library alternatives tool. Opt-in
	// (NOT MarkAlwaysOn); the model discovers
	// it via tool_search. The Finder uses a
	// built-in catalog of ~25 curated mappings.
	libFinder := library.NewFinder()
	registry.MustRegister(tools.NewCheckLibraryAlternatives(libFinder).Spec())

	// F24: targeted file operations. All five
	// tools are opt-in (NOT MarkAlwaysOn); the
	// model discovers them via tool_search. They
	// save tokens by reading/editing specific line
	// ranges instead of entire files.
	registry.MustRegister(tools.NewReadLines(home).Spec())
	registry.MustRegister(tools.NewReadContext(home).Spec())
	registry.MustRegister(tools.NewEditLine(home).Spec())
	registry.MustRegister(tools.NewInsertAfter(home).Spec())
	registry.MustRegister(tools.NewDeleteLines(home).Spec())

	// F7 + F8 status bar. The goal line is rendered
	// above the credits line when both are present.
	statusFn := func() string {
		goal := goalSvc.StatusLine(context.Background())
		cred := credits.StatusLine(tracker, provider.Name())
		// F34: live token counter and cost projection.
		tokens := ""
		costStr := ""
		if draftStats != nil {
			turns := draftStats.Snapshot()
			total := stats.Sum(turns)
			totalTokens := total.TokensIn + total.TokensOut
			if totalTokens > 0 {
				tokens = compactNum(totalTokens)
				// Calculate cost from current model rates.
				r, _ := credits.RateFor(provider.Name())
				inputCost := float64(total.TokensIn) / 1000.0 * r.InputPer1k
				outputCost := float64(total.TokensOut) / 1000.0 * r.OutputPer1k
				totalCost := inputCost + outputCost
				if totalCost > 0 {
					costStr = fmt.Sprintf("$%.4f", totalCost)
				}
			}
		}
		var lines []string
		if goal != "" {
			lines = append(lines, goal)
		}
		var bottom []string
		if cred != "" {
			bottom = append(bottom, cred)
		}
		if tokens != "" {
			tokStr := tokens
			if costStr != "" {
				tokStr += " │ " + costStr
			}
			bottom = append(bottom, "tok: "+tokStr)
		}
		if len(bottom) > 0 {
			lines = append(lines, strings.Join(bottom, " │ "))
		}
		return strings.Join(lines, "\n")
	}

	// F12: external event sink. The consult
	// tool and the /council slash command emit
	// ConsultEvent to this channel even when no
	// Run is in progress. The TUI pumps it via
	// waitForExternalEvent and renders the
	// [council: ...] marker in the transcript.
	extCh := make(chan agent.Event, 16)
	loop.SetExternalSink(extCh)

	// F30: create provider manager, load persisted
	// hidden-models state, and reload the providers list
	// from config.toml so model filtering works immediately.
	provMgr := providers.NewManager(home)
	provMgr.Reload()
	provMgr.LoadHiddenState()

	// Scan all configured providers for models in the
	// background so they're ready when the user opens
	// /models or /model. Non-blocking — the TUI starts
	// immediately, models appear as they're discovered.
	go provMgr.ScanModels(caps)

	model := tui.New(tui.Options{
		Home:         home,
		DataDir:      dataDir,
		Agent:        loop,
		LLM:          provider,
		Commands:     mergedCommands,
		StatusFn:     statusFn,
		ExtCh:        extCh,
		ShellRunner:  shellescape.NewRunner(home),
		Tracker:      fileops.NewTracker(200),
		ModelSwapper: loop,
		ModelLister:  loop,
		ModelSwapFn: func(modelID, providerName string) (llm.Provider, error) {
			// Build a new provider with the target model.
			// Look up the provider's base URL and API key
			// from the config.toml providers list.
			swapCfg := cfg
			swapCfg.Model = modelID
			if providerName != "" {
				globalPath, _ := config.FindTomlPaths(home, ".")
				tc, err := config.LoadToml(globalPath)
				if err == nil {
					for _, pc := range tc.Providers {
						if pc.Name == providerName {
							swapCfg.BaseURL = pc.BaseURL
							swapCfg.APIKey = pc.APIKey
							swapCfg.Provider = pc.Type
							break
						}
					}
				}
			}
			return buildProvider(swapCfg, home, caps)
		},
		SessionStore:       sessStore,
		StatsRecorder:      draftStats,
		ProviderMgr:        provMgr,
		CapabilityRegistry: caps,
		GoalService:        goalSvc,
		ToolRegistry:       registry,
	})

	program := tea.NewProgram(model, tea.WithAltScreen())

	pumpDone := make(chan struct{})
	go func() {
		defer recoverAndLog(home)()
		defer close(pumpDone)
		for req := range askCh {
			program.Send(tui.AskRequestMsgFrom(req))
		}
	}()

	if _, err := program.Run(); err != nil {
		logCrash(home, fmt.Errorf("tui error: %w", err))
		fmt.Fprintf(os.Stderr, "tui error: %v\n", err)
		os.Exit(1)
	}
	startPostTUIShutdownTimer(home, 200*time.Millisecond)
	close(askCh)
	<-pumpDone
}

// runBatch executes a single prompt without TUI, printing the
// assistant response to stdout. Used for CI/CD and scripting.
func runBatch(prompt, home, providerFlag, keyFlag, baseFlag, modelFlag string, echoFlag, debugFlag bool, draftMode, draftModel string) {
	// Echo shortcut.
	if echoFlag {
		keyFlag = ""
		providerFlag = config.ProviderEcho
	}

	cfg, err := config.Load(config.FlagOverrides{
		Provider: providerFlag,
		APIKey:   keyFlag,
		BaseURL:  baseFlag,
		Model:    modelFlag,
		Debug:    boolPtr(debugFlag),
	})
	if err != nil {
		fatal("load config", err)
	}

	caps, err := llm.NewCapabilityRegistryFromSources(home, nil)
	if err != nil {
		fatal("load capabilities", err)
	}

	p, err := buildProvider(cfg, home, caps)
	if err != nil {
		fatal("build provider", err)
	}

	// Build the agent loop.
	reg := tools.NewRegistry()
	l, err := agent.NewLoop(agent.LoopConfig{
		Provider: p,
		Registry: reg,
		Caps:     caps,
		MaxSteps: 25,
	})
	if err != nil {
		fatal("agent loop", err)
	}

	ch, err := l.Run(context.Background(), prompt)
	if err != nil {
		fatal("agent run", err)
	}

	// Consume events, print MessageEvent texts.
	for ev := range ch {
		switch e := ev.(type) {
		case agent.MessageEvent:
			fmt.Println(e.Text)
		case agent.ErrorEvent:
			fmt.Fprintf(os.Stderr, "error: %v\n", e.Err)
			os.Exit(1)
		}
	}
}

// buildChildToolRegistry builds a small self-contained
// tool registry rooted at root for Darwin child agents.
// Every file/code tool resolves paths inside root (the
// child's git worktree), giving real per-candidate
// isolation. Children deliberately do NOT get the
// darwin tool (no recursion), ask_user (no TUI
// channel), goal/memory (shared state), or tool_search
// (no FTS index) — so everything registered here is
// marked always-on.
func buildChildToolRegistry(root string) *tools.Registry {
	reg := tools.NewRegistry()
	reg.MustRegister(tools.NewReadImage(root, 0).Spec())
	reg.MustRegister(tools.NewSearchCode(root).Spec())
	reg.MustRegister(tools.NewReadZip(root, 0).Spec())
	reg.MustRegister(tools.NewReadDocx(root, 0).Spec())
	reg.MustRegister(tools.NewReadXlsx(root, 0).Spec())
	reg.MustRegister(tools.NewReadPdf(root, 0).Spec())
	reg.MustRegister(tools.NewEditDocx(root).Spec())
	reg.MustRegister(tools.NewEditXlsx(root).Spec())
	reg.MustRegister(tools.NewFileOps(root).Spec())
	reg.MustRegister(tools.NewReadLines(root).Spec())
	reg.MustRegister(tools.NewReadContext(root).Spec())
	reg.MustRegister(tools.NewEditLine(root).Spec())
	reg.MustRegister(tools.NewInsertAfter(root).Spec())
	reg.MustRegister(tools.NewDeleteLines(root).Spec())
	reg.MustRegister(tools.NewCtxExecuteTool(ctxexec.New(root), root).Spec())
	for _, name := range reg.Names() {
		reg.MarkAlwaysOn(name)
	}
	return reg
}

func buildProvider(cfg config.Config, home string, caps *llm.CapabilityRegistry) (llm.Provider, error) {
	if cfg.IsEcho() {
		return llm.NewEcho(cfg.Model)
	}
	if cfg.Provider == config.ProviderOpencode {
		// F15: opencode headless gateway. The
		// provider wraps an OpenAI-compat client
		// pointed at the local gateway URL. Model
		// discovery happens after construction
		// (caller probes /v1/models and registers
		// in the capability registry).
		p, err := llm.NewOpencode(llm.OpencodeConfig{
			BaseURL:      cfg.BaseURL,
			APIKey:       cfg.APIKey,
			Model:        cfg.Model,
			Capabilities: caps,
		})
		if err != nil {
			return nil, fmt.Errorf("buildProvider opencode: %w", err)
		}
		// Probe /v1/models and register discovered
		// models in the F16 capability pool. This
		// is best-effort; if the gateway is down
		// we still return the provider — the user
		// can still use an explicit --model.
		models, _ := p.ProbeModels(context.Background())
		if len(models) > 0 {
			log.Printf("F15: discovered %d model(s) from opencode gateway", len(models))
		}
		return p, nil
	}
	return llm.NewOpenAI(llm.OpenAIConfig{
		BaseURL:      cfg.BaseURL,
		APIKey:       cfg.APIKey,
		Model:        cfg.Model,
		Timeout:      cfg.Timeout,
		Capabilities: caps,
	})
}

func boolPtr(b bool) *bool { return &b }

// tierRulesFromToml converts config.toml [[model_tiers]]
// entries into tier.Rule values.
func tierRulesFromToml(t config.TomlConfig) []tier.Rule {
	if len(t.ModelTiers) == 0 {
		return nil
	}
	out := make([]tier.Rule, 0, len(t.ModelTiers))
	for _, r := range t.ModelTiers {
		out = append(out, tier.Rule{Pattern: r.Pattern, Tier: r.Tier})
	}
	return out
}

// pickSmallestSmallTierModel scans the capability registry for
// the model with the smallest parsed (active) parameter count
// that classifies as small-tier. Used as the second priority
// for draft-model selection (after an explicit --draft-model /
// config, before the price-based fallback).
func pickSmallestSmallTierModel(caps *llm.CapabilityRegistry, exclude string, rules []tier.Rule) (string, bool) {
	best := ""
	bestParams := 0.0
	for _, m := range caps.All() {
		if m.ID == exclude {
			continue
		}
		params, ok := tier.ParseParams(m.ID)
		if !ok {
			continue
		}
		if tier.Classify(m.ID, m.InputCost, m.OutputCost, rules) != tier.Small {
			continue
		}
		if best == "" || params < bestParams {
			best, bestParams = m.ID, params
		}
	}
	return best, best != ""
}

// compactNum formats large numbers as "1.2k" etc.
func compactNum(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fm", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func initAppLog(dataDir string) *os.File {
	logsDir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(logsDir, "supercli.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	log.SetOutput(f)
	return f
}

// buildDraftWiring assembles the F11 draft policy +
// provider. The provider is a SECOND llm.OpenAI
// instance (or llm.Echo when the verifier is echo)
// configured with a different Model id. The model is
// chosen in this priority order:
//
//  1. --draft-model flag (verbatim, if set)
//  2. F16 CapabilityRegistry.SuggestCheapestForTask
//     (the cheapest tool-using model that isn't the
//     verifier itself)
//
// Returns (nil, nil) when F11 is off or no candidate
// exists — silent fallback per D1.
func buildDraftWiring(modeFlag, modelFlag string, verifier llm.Provider, caps *llm.CapabilityRegistry, cfg config.Config, tierRules []tier.Rule) (*draft.Policy, llm.Provider) {
	mode, err := draft.ParseMode(modeFlag)
	if err != nil {
		log.Printf("F11: bad --draft-mode %q, defaulting to off: %v", modeFlag, err)
		return nil, nil
	}
	if mode == draft.ModeOff {
		return nil, nil
	}
	verifierName := verifier.Name()
	var draftModel string
	if modelFlag != "" {
		draftModel = modelFlag
	} else if caps != nil {
		// Priority: smallest parsed-param model that
		// classifies small-tier (internal/tier), then the
		// price-based cheapest-for-task fallback.
		if picked, ok := pickSmallestSmallTierModel(caps, verifierName, tierRules); ok {
			draftModel = picked
			log.Printf("F11: auto-picked smallest small-tier draft model %q (verifier=%q)", draftModel, verifierName)
		} else if picked, ok := caps.SuggestCheapestForTask("plan", verifierName); ok {
			draftModel = picked
			log.Printf("F11: auto-picked draft model %q (verifier=%q)", draftModel, verifierName)
		}
	}
	if draftModel == "" {
		log.Printf("F11: no draft model available (verifier=%q); F11 disabled silently", verifierName)
		return nil, nil
	}
	if draftModel == verifierName {
		log.Printf("F11: draft model %q == verifier; F11 disabled silently (per D1 rule)", draftModel)
		return nil, nil
	}
	policy, err := draft.NewPolicy(mode, draftModel, verifierName, nil)
	if err != nil {
		log.Printf("F11: policy build failed: %v; F11 disabled silently", err)
		return nil, nil
	}
	// Build a second provider instance with the same
	// transport but a different model id. The
	// verifier's provider and the draft's provider
	// share API key, base URL, etc.
	var prov llm.Provider
	if cfg.IsEcho() {
		// Echo mode: build a separate echo for the
		// draft, which is fine for tests / offline.
		prov, _ = llm.NewEcho("draft:" + draftModel)
	} else {
		prov, err = llm.NewOpenAI(llm.OpenAIConfig{
			BaseURL:      cfg.BaseURL,
			APIKey:       cfg.APIKey,
			Model:        draftModel,
			Capabilities: caps,
		})
		if err != nil {
			log.Printf("F11: draft provider build failed: %v; F11 disabled silently", err)
			return nil, nil
		}
	}
	return policy, prov
}

// buildConsultCouncil assembles the F12 cross-model
// consultation engine. The samples are N model ids
// pulled from the F16 CapabilityRegistry
// (SuggestCheapestN, cheapest-first, excluding the
// judge itself). The judge is the running main
// provider (the user is already paying for it).
//
// The first-cut implementation uses the SAME
// transport (OpenAI-compat) for all samples — we
// rebuild llm.OpenAI with a different Model id per
// sample, sharing the user's API key + base URL.
// This is the cheapest, most portable design: any
// OpenAI-compat endpoint the user has configured
// can serve every council member. When the user
// later adds Anthropic / Ollama / Groq adapters
// (F15 territory), the per-provider factory will
// branch on caps.Provider(id).
//
// Returns nil when the registry is empty, when no
// candidates are available, or when provider
// construction fails for every id. The consult
// tool and the /council slash command both
// gracefully degrade to "not wired" in that case.
func buildConsultCouncil(n int, judge llm.Provider, caps *llm.CapabilityRegistry, cfg config.Config) *consult.Council {
	if n <= 0 {
		n = 3
	}
	if caps == nil || judge == nil {
		return nil
	}
	judgeName := judge.Name()
	ids := caps.SuggestCheapestN("consult", judgeName, n)
	if len(ids) == 0 {
		log.Printf("F12: no sample models available (judge=%q); consult disabled", judgeName)
		return nil
	}
	samples := make([]llm.Provider, 0, len(ids))
	for _, id := range ids {
		var prov llm.Provider
		if cfg.IsEcho() {
			prov, _ = llm.NewEcho("consult-sample:" + id)
		} else {
			p, err := llm.NewOpenAI(llm.OpenAIConfig{
				BaseURL:      cfg.BaseURL,
				APIKey:       cfg.APIKey,
				Model:        id,
				Capabilities: caps,
			})
			if err != nil {
				log.Printf("F12: sample provider %q build failed: %v", id, err)
				continue
			}
			prov = p
		}
		if prov != nil {
			samples = append(samples, prov)
		}
	}
	if len(samples) == 0 {
		log.Printf("F12: no sample providers built; consult disabled")
		return nil
	}
	log.Printf("F12: council assembled: %d sample(s) (judge=%q)", len(samples), judgeName)
	return &consult.Council{
		Samples: samples,
		Judge:   judge,
	}
}

// darwinCommands returns the slash-command table for the
// TUI. Currently registers `/darwin` which invokes the
// DarwinTool synchronously and returns the rendered
// result. dt is the same DarwinTool instance registered
// in the tool registry, so the slash command and the
// model-invoked tool share state.
func darwinCommands(dt *darwin.DarwinTool) map[string]tui.SlashHandler {
	return map[string]tui.SlashHandler{
		"darwin": func(ctx context.Context, args string) (string, error) {
			prompt, poolSize := parseDarwinArgs(args, 3)
			if strings.TrimSpace(prompt) == "" {
				return "usage: /darwin [N] <prompt>\nexample: /darwin 3 fix failing tests", nil
			}
			raw, err := json.Marshal(map[string]any{
				"prompt":     prompt,
				"pool_size":  poolSize,
				"auto_merge": false,
				"judge":      "composite",
			})
			if err != nil {
				return "", err
			}
			res, _ := dt.Spec().Fn(ctx, raw)
			if res.Err != nil {
				return "", res.Err
			}
			return res.Text, nil
		},
	}
}

// goalCommands returns the `/goal` slash-command
// handler. The subcommand (set/list/show/tasks/...) is
// the first whitespace-separated token of args; the
// rest is passed to that action.
//
// Examples:
//
//	/goal set ship F8
//	/goal show
//	/goal tasks add design doc
//	/goal tasks done 1
//	/goal note we have a draft
//	/goal done
//
// All mutations call Refresh on the service so the
// status line and the next injected prompt see the
// new state.
func goalCommands(svc *goal.Service) map[string]tui.SlashHandler {
	run := func(_ context.Context, args string) (string, error) {
		return runGoalCommand(svc, args)
	}
	return map[string]tui.SlashHandler{
		"goal": run,
	}
}

// runGoalCommand parses "/goal <subcmd> [args...]" and
// dispatches to the right goal.Service / tools.GoalTool
// action. Returns a Markdown string the TUI prints.
func runGoalCommand(svc *goal.Service, args string) (string, error) {
	args = strings.TrimSpace(args)
	if args == "" {
		return goalUsage(), nil
	}
	fields := strings.Fields(args)
	sub := strings.ToLower(fields[0])
	rest := ""
	if len(fields) > 1 {
		rest = strings.TrimSpace(strings.TrimPrefix(args, fields[0]))
	}
	ctx := context.Background()

	switch sub {
	case "set":
		title := rest
		if title == "" {
			return "goal: /goal set requires a title", nil
		}
		g, err := svc.Set(ctx, title, "", "", "")
		if err != nil {
			return "goal: " + err.Error(), nil
		}
		return fmt.Sprintf("active goal: %s (%s)", g.Title, g.ID), nil

	case "list":
		all, err := svc.List(ctx)
		if err != nil {
			return "goal: " + err.Error(), nil
		}
		if len(all) == 0 {
			return "no goals yet. /goal set <title>", nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d goal(s):\n", len(all))
		for _, g := range all {
			fmt.Fprintf(&b, "  %s  %-9s  %s\n", g.ID, g.Status, shortenLine(g.Title, 60))
		}
		return b.String(), nil

	case "show":
		id := rest
		g, err := resolveGoal(svc, ctx, id)
		if err != nil {
			return "goal: " + err.Error(), nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s  [%s]  %s\n", g.ID, g.Status, g.Title)
		if g.Description != "" {
			fmt.Fprintf(&b, "  %s\n", g.Description)
		}
		if g.SuccessCriteria != "" {
			fmt.Fprintf(&b, "  criteria: %s\n", g.SuccessCriteria)
		}
		if g.Notes != "" {
			fmt.Fprintf(&b, "  notes: %s\n", shortenLine(g.Notes, 200))
		}
		return b.String(), nil

	case "tasks":
		// sub-sub: add, done, skip, list
		return runGoalTasks(svc, rest)

	case "decompose":
		title := rest
		if title == "" {
			return "goal: /goal decompose <title>", nil
		}
		tasks := goal.HeuristicFromTitle(title)
		if len(tasks) == 0 {
			return "goal: no tasks produced", nil
		}
		for _, t := range tasks {
			if _, err := svc.AddTask(ctx, "", t); err != nil {
				return "goal: " + err.Error(), nil
			}
		}
		var b strings.Builder
		fmt.Fprintf(&b, "decomposed into %d tasks:\n", len(tasks))
		for i, t := range tasks {
			fmt.Fprintf(&b, "  %d. %s\n", i+1, t)
		}
		return b.String(), nil

	case "note":
		text := rest
		if text == "" {
			return "goal: /goal note <text>", nil
		}
		if err := svc.AppendNote(ctx, "", text); err != nil {
			return "goal: " + err.Error(), nil
		}
		return "note appended.", nil

	case "done":
		if err := svc.SetStatus(ctx, "", goal.StatusDone); err != nil {
			return "goal: " + err.Error(), nil
		}
		return "active goal marked done.", nil

	case "abandon":
		if err := svc.SetStatus(ctx, "", goal.StatusAbandoned); err != nil {
			return "goal: " + err.Error(), nil
		}
		return "active goal abandoned.", nil

	case "help", "?":
		return goalUsage(), nil

	default:
		return goalUsage(), nil
	}
}

// runGoalTasks handles "/goal tasks <add|done|skip|list>".
func runGoalTasks(svc *goal.Service, args string) (string, error) {
	args = strings.TrimSpace(args)
	if args == "" {
		// default: list
		args = "list"
	}
	fields := strings.Fields(args)
	sub := strings.ToLower(fields[0])
	rest := ""
	if len(fields) > 1 {
		rest = strings.TrimSpace(strings.TrimPrefix(args, fields[0]))
	}
	ctx := context.Background()

	switch sub {
	case "list":
		tasks, err := svc.ListTasks(ctx, "")
		if err != nil {
			return "goal: " + err.Error(), nil
		}
		if len(tasks) == 0 {
			return "no tasks for active goal.", nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d task(s):\n", len(tasks))
		for _, t := range tasks {
			fmt.Fprintf(&b, "  [%s] %d. %s\n", taskMark(t.Status), t.Seq, t.Title)
		}
		return b.String(), nil

	case "add":
		if rest == "" {
			return "goal: /goal tasks add <title>", nil
		}
		t, err := svc.AddTask(ctx, "", rest)
		if err != nil {
			return "goal: " + err.Error(), nil
		}
		return fmt.Sprintf("added task %d: %s", t.Seq, t.Title), nil

	case "done":
		if rest == "" {
			return "goal: /goal tasks done <seq>", nil
		}
		seq, err := strconv.Atoi(strings.TrimSpace(rest))
		if err != nil {
			return "goal: invalid seq: " + rest, nil
		}
		if err := svc.SetTaskStatus(ctx, "", seq, goal.TaskDone); err != nil {
			return "goal: " + err.Error(), nil
		}
		return fmt.Sprintf("task %d -> done", seq), nil

	case "skip":
		if rest == "" {
			return "goal: /goal tasks skip <seq>", nil
		}
		seq, err := strconv.Atoi(strings.TrimSpace(rest))
		if err != nil {
			return "goal: invalid seq: " + rest, nil
		}
		if err := svc.SetTaskStatus(ctx, "", seq, goal.TaskSkipped); err != nil {
			return "goal: " + err.Error(), nil
		}
		return fmt.Sprintf("task %d -> skipped", seq), nil

	default:
		return "goal: tasks subcommand must be list, add, done, or skip", nil
	}
}

// mergedSlashCommands returns darwin + goal commands.
// Goal gets priority on key collision (defensive; they
// don't currently share keys).
func mergedSlashCommands(dt *darwin.DarwinTool, svc *goal.Service) map[string]tui.SlashHandler {
	out := darwinCommands(dt)
	for k, v := range goalCommands(svc) {
		out[k] = v
	}
	return out
}

func goalUsage() string {
	return "usage: /goal <set|list|show|tasks|note|done|abandon|decompose|help> [args]\n" +
		"  set <title>             start a new active goal (auto-pauses prior)\n" +
		"  list                    show all goals\n" +
		"  show [id]               show one goal (default: active)\n" +
		"  tasks [list]            list active goal's tasks (default)\n" +
		"  tasks add <title>       append a task\n" +
		"  tasks done <seq>        mark task done\n" +
		"  tasks skip <seq>        skip a task\n" +
		"  note <text>             append a timestamped note\n" +
		"  done                    mark the active goal done\n" +
		"  abandon                 abandon the active goal\n" +
		"  decompose <title>       split a title into tasks (heuristic)"
}

func resolveGoal(svc *goal.Service, ctx context.Context, id string) (*goal.Goal, error) {
	if id == "" {
		g := svc.Active()
		if g == nil {
			return nil, fmt.Errorf("no active goal")
		}
		return g, nil
	}
	return svc.Goal(ctx, id)
}

func taskMark(s goal.Status) string {
	switch s {
	case goal.TaskDone:
		return "x"
	case goal.TaskInProgress:
		return ">"
	case goal.TaskSkipped:
		return "~"
	default:
		return " "
	}
}

func shortenLine(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// parseDarwinArgs splits "/darwin 3 fix the bug" into
// (prompt="fix the bug", poolSize=3). Default pool is 3.
func parseDarwinArgs(args string, defaultPool int) (string, int) {
	args = strings.TrimSpace(args)
	if args == "" {
		return "", defaultPool
	}
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return "", defaultPool
	}
	if n, err := strconv.Atoi(fields[0]); err == nil && n > 0 {
		return strings.TrimSpace(strings.TrimPrefix(args, fields[0])), n
	}
	return args, defaultPool
}

// runStatus prints a one-shot summary of credit usage
// and the most recent audit events. Used by --status
// (F7).
func runStatus(home string, cs *credits.Storage) error {
	ctx := context.Background()
	today, err := cs.DailyTotal(ctx)
	if err != nil {
		return fmt.Errorf("daily total: %w", err)
	}
	fmt.Printf("supercli %s — status\n", version)
	fmt.Printf("home: %s\n", home)
	fmt.Printf("daily tokens (UTC day): %d\n", today)
	events, err := credits.Tail(home, 10)
	if err != nil {
		return fmt.Errorf("audit tail: %w", err)
	}
	if len(events) == 0 {
		fmt.Println("audit: (empty)")
		return nil
	}
	fmt.Println("audit (last 10 events):")
	for _, e := range events {
		ts := time.Unix(0, e.TS).UTC().Format("15:04:05")
		path := e.Path
		if path == "" {
			path = "-"
		}
		res := e.Result
		if res == "" {
			res = "?"
		}
		fmt.Printf("  %s  %-12s  %-5s  %s  %s\n",
			ts, e.Tool, res, path, truncate(e.Args, 60))
	}
	return nil
}

// truncate shortens a string to n characters with an
// ellipsis suffix. Used by --status to keep the audit
// column narrow.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

// runListModels prints every model the registry
// knows about. With refresh=true, the registry is
// augmented with the /v1/models list from the
// configured provider first; the result is shown
// in-memory only (no save), so subsequent
// invocations without --refresh still see the
// user catalog + seed + probe cache as before.
//
// The output columns are deliberately compact: a
// developer running --list-models in a terminal
// wants to see the field at a glance. Detailed
// metadata lives behind --model-info.
func runListModels(caps *llm.CapabilityRegistry, baseURL, apiKey, providerName string, refresh bool) {
	if refresh {
		if baseURL == "" {
			fmt.Println("# --refresh: no base URL configured; showing cached registry only")
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			ids, err := llm.ListProviderModels(ctx, baseURL, apiKey)
			cancel()
			if err != nil {
				log.Printf("list-models: provider list failed: %v", err)
			} else {
				for _, id := range ids {
					m := llm.HeuristicCapabilities(id)
					if providerName != "" {
						m.Provider = providerName
					}
					caps.Register(m)
				}
			}
		}
	}
	models := caps.All()
	if len(models) == 0 {
		fmt.Println("(no models known)")
		return
	}
	fmt.Printf("%-40s %-10s %-6s %-5s %-5s %-7s %-10s\n",
		"ID", "PROVIDER", "VISION", "TOOL", "STREAM", "REASON", "SOURCE")
	for _, m := range models {
		id := m.ID
		if len(id) > 40 {
			id = id[:37] + "..."
		}
		fmt.Printf("%-40s %-10s %-6v %-5v %-5v %-7v %-10s\n",
			id, m.Provider, m.Vision, m.ToolUse, m.Stream, m.Reasoning, m.Source)
	}
}

// runModelInfo prints detailed capability info
// for a single model id. If the registry does not
// have the model, we fall back to the heuristic
// so the user gets a best-guess answer (clearly
// labelled as such).
func runModelInfo(caps *llm.CapabilityRegistry, id string) {
	m, ok := caps.Get(id)
	heuristicOnly := false
	if !ok {
		m = llm.HeuristicCapabilities(id)
		heuristicOnly = true
	}
	fmt.Printf("ID            %s\n", m.ID)
	if m.Provider != "" {
		fmt.Printf("Provider      %s\n", m.Provider)
	}
	fmt.Printf("Vision        %v\n", m.Vision)
	fmt.Printf("ToolUse       %v\n", m.ToolUse)
	fmt.Printf("Stream        %v\n", m.Stream)
	fmt.Printf("Reasoning     %v\n", m.Reasoning)
	if m.ContextLength > 0 {
		fmt.Printf("Context       %d tokens\n", m.ContextLength)
	}
	if m.InputCost > 0 {
		fmt.Printf("Input cost    $%.4f / 1M tokens\n", m.InputCost)
	}
	if m.OutputCost > 0 {
		fmt.Printf("Output cost   $%.4f / 1M tokens\n", m.OutputCost)
	}
	if m.Notes != "" {
		fmt.Printf("Notes         %s\n", m.Notes)
	}
	if !m.LastVerified.IsZero() {
		fmt.Printf("Last verified %s\n", m.LastVerified.UTC().Format("2006-01-02"))
	}
	fmt.Printf("Source        %s\n", m.Source)
	if heuristicOnly {
		fmt.Println("Note: not in registry; showing heuristic guess. Run --refresh to learn from the provider.")
	}
}

// runDoctor prints a checklist of the things that must
// be true for SuperCli to run well. Used by --doctor
// (F7). Exits 0 even on warnings — the goal is to
// surface the situation, not to gate the user.
//
// F18: when a staleness report is provided, it is
// appended at the end of the checklist.
func runDoctor(home, dataDir string, _ *credits.Storage, report *freshness.Report) {
	rep := doctor.Run(context.Background(), doctor.Env{Version: version, Home: home, DataDir: dataDir})
	fmt.Println(doctor.RenderPlain(rep))
	// F18: staleness report from the freshness checker.
	if report != nil {
		txt := freshness.FormatReport(*report)
		if txt != "" {
			fmt.Println(txt)
		}
	}
	fmt.Println()
	fmt.Println("Use --status to inspect credit usage and the audit log.")
}

func fatal(what string, err error) {
	log.Fatalf("%s: %v", what, err)
}

// checkDirWritable verifies the directory exists and is
// writable by creating and removing a temp file.
func checkDirWritable(dir string) error {
	tmp := filepath.Join(dir, ".write_test")
	if err := os.WriteFile(tmp, []byte("x"), 0644); err != nil {
		return fmt.Errorf("%s: %w", dir, err)
	}
	return os.Remove(tmp)
}

// startPostTUIShutdownTimer bounds slow cleanup after the screen has
// already been restored. It is deliberately started only after
// program.Run returns, so it can never close the live TUI like the old
// pre-run watchdog did. SQLite WAL and OS file handles remain safe if
// this fires: committed transactions are durable and the OS closes
// handles on process exit.
func startPostTUIShutdownTimer(home string, d time.Duration) {
	if d <= 0 {
		return
	}
	go func() {
		defer recoverAndLog(home)()
		t := time.NewTimer(d)
		defer t.Stop()
		<-t.C
		os.Exit(0)
	}()
}

// discoverSkillsForDoctor scans the home directory for
// SKILL.md files and returns freshness.SkillEntry values
// with file mtimes. Used by --doctor to report stale
// skills without requiring a running provider.
func discoverSkillsForDoctor(home string) []freshness.SkillEntry {
	d := tools.NewDiscoverer(home, home)
	skills, err := d.Discover()
	if err != nil {
		return nil
	}
	out := make([]freshness.SkillEntry, 0, len(skills))
	for _, s := range skills {
		fi, err := os.Stat(s.Path)
		if err != nil {
			continue
		}
		out = append(out, freshness.SkillEntry{
			Name:     s.Name,
			Path:     s.Path,
			Modified: fi.ModTime(),
		})
	}
	return out
}

// ultraworkGoalAdapter bridges *goal.Service to the
// ultrawork.GoalGate interface. Defined here (not in the
// ultrawork package) so the ultrawork package does not
// have to import goal.
type ultraworkGoalAdapter struct {
	svc *goal.Service
}

func (g ultraworkGoalAdapter) ActiveID() string {
	if g.svc == nil {
		return ""
	}
	a := g.svc.Active()
	if a == nil {
		return ""
	}
	return a.ID
}

func (g ultraworkGoalAdapter) ActiveTitle() string {
	if g.svc == nil {
		return ""
	}
	a := g.svc.Active()
	if a == nil {
		return ""
	}
	return a.Title
}

func (g ultraworkGoalAdapter) UnfinishedTasks(ctx context.Context) int {
	if g.svc == nil {
		return 0
	}
	a := g.svc.Active()
	if a == nil {
		return 0
	}
	tasks, err := g.svc.ListTasks(ctx, a.ID)
	if err != nil {
		return 0
	}
	n := 0
	for _, t := range tasks {
		if t.Status == goal.TaskDone || t.Status == goal.TaskSkipped {
			continue
		}
		n++
	}
	return n
}

// ultraworkCreditAdapter bridges *credits.Tracker to the
// ultrawork.CreditGate interface. Defined here (not in
// the ultrawork package) so the ultrawork package does
// not have to import credits.
type ultraworkCreditAdapter struct {
	tracker *credits.Tracker
}

func (c ultraworkCreditAdapter) Remaining(ctx context.Context) (int64, int64) {
	if c.tracker == nil {
		return 0, 0
	}
	// credits.Tracker has no Remaining() method, only
	// Used(). Compute the gap from the budget.
	budget := c.tracker.Budget()
	sessUsed, dayUsed := c.tracker.Used()
	sess := budget.PerSession - sessUsed
	if sess < 0 {
		sess = 0
	}
	day := budget.PerDay - dayUsed
	if day < 0 {
		day = 0
	}
	return sess, day
}

func (c ultraworkCreditAdapter) HasBudget() bool {
	if c.tracker == nil {
		return false
	}
	b := c.tracker.Budget()
	return b.PerSession > 0 || b.PerDay > 0
}

func usage() {
	fmt.Fprintf(os.Stderr, `supercli %s — portable AI CLI agent

Usage:
  supercli [flags]

Flags:
  --home PATH                     override the home directory (also: $SUPERCLI_HOME)
  --provider P                    LLM provider: openai (default) or echo
  --model M                       model id (default: $SUPERCLI_LLM_MODEL or gpt-4o-mini)
  --key K                         API key (overrides SUPERCLI_LLM_API_KEY)
  --base-url U                    base URL (overrides SUPERCLI_LLM_BASE_URL)
  --echo                          force echo provider (useful for offline testing)
  --debug                         verbose logging
  --status                        print credit usage + audit tail and exit
  --doctor                        run environment checks and exit
  --list-models                   print known model capabilities and exit (add --refresh to re-fetch)
  --refresh                       with --list-models, re-fetch /v1/models and re-probe
  --model-info ID                 print details for a single model and exit
  --max-credits-per-session N     cap total tokens per session (0 = no cap)
  --max-credits-per-day N         cap total tokens per UTC day (0 = no cap)
  --draft-mode MODE               F11 draft mode: off|always|balanced|critical (default critical)
  --draft-model ID                F11 draft model id (default: auto-pick cheapest from F16 registry)
  --version                       print version and exit
  -h, --help                      show this help

Env vars:
  SUPERCLI_LLM_PROVIDER, SUPERCLI_LLM_API_KEY, SUPERCLI_LLM_BASE_URL,
  SUPERCLI_LLM_MODEL, SUPERCLI_LLM_TEMPERATURE, SUPERCLI_LLM_STREAM,
  SUPERCLI_LLM_TIMEOUT, SUPERCLI_DEBUG, SUPERCLI_HOME

Data is stored under <home>/.supercli/ (default: <cwd>/.supercli/).
Nothing is written to %%APPDATA%% or the user home without consent.
`, version)
}
