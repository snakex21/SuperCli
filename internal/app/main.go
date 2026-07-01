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
package app

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/account/codexauth"
	"supercli/internal/account/credits"
	"supercli/internal/account/pricing"
	"supercli/internal/account/tier"
	"supercli/internal/agent"
	"supercli/internal/agent/darwin"
	"supercli/internal/agent/reflect"
	"supercli/internal/agent/ultrawork"
	"supercli/internal/llm"
	"supercli/internal/llm/consult"
	"supercli/internal/llm/draft"
	"supercli/internal/llm/prompt"
	"supercli/internal/llm/providers"
	"supercli/internal/llm/shuffler"
	"supercli/internal/storage"
	"supercli/internal/storage/freshness"
	"supercli/internal/storage/goal"
	"supercli/internal/storage/library"
	"supercli/internal/storage/memory"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
	"supercli/internal/system/doctor"
	"supercli/internal/system/stats"
	"supercli/internal/tools"
	"supercli/internal/tools/ctxexec"
	"supercli/internal/tools/fileops"
	"supercli/internal/tools/sandbox"
	"supercli/internal/tools/shellescape"
	"supercli/internal/ui/tui"
)

const version = "0.6.0"

// codexAuthMgr handles ChatGPT-subscription (Codex) OAuth tokens.
// Set once at startup from the [codex_auth] config section;
// buildProvider and the /login and /logout commands use it.
var codexAuthMgr *codexauth.Manager

// initCodexAuth builds the codex auth manager from config.
func initCodexAuth(dataDir string, t config.TomlConfig) {
	codexAuthMgr = codexauth.NewManager(dataDir, codexauth.Options{
		ClientID:   t.CodexAuth.ClientID,
		Issuer:     t.CodexAuth.Issuer,
		BackendURL: t.CodexAuth.BackendURL,
	})
}

// supercliSystemPromptBase is the layered system prompt
// shared by the main loop and any F6 Darwin children. It
// defaults to core + extended guidance; main() rebuilds it
// once the model's tier is known — small-tier models get the
// core only (see internal/prompt and internal/tier).
var supercliSystemPromptBase = prompt.Build(false)
var supercliCoordinatorMode bool

// memoryBriefing is the code-built session-start briefing (user
// preferences, project card, recent session summaries, other
// projects). Set once in main() before the loop is created.
var memoryBriefing string

// workingDirNote states the ACTUAL sandbox root (the BaseDir the
// file tools enforce) so the model uses the right path on its
// first file call. Derived in main() from the same resolved home
// the tools get — never hardcoded — and injected last so it wins
// over any conflicting project path a memory fact might mention.
var workingDirNote string

// memoryAutoSaveInstruction backs the B4 contract: the model is
// told to save a task-log entry after each finished task; the
// AutoSaver in code covers sessions where it forgets.
const memoryAutoSaveInstruction = "Memory: after completing a task, call remember with " +
	"type=task-log summarizing WHAT you did, WHY, and which files you touched. " +
	"Save user preferences with type=preference (scope=global). Use recall at the " +
	"start of non-trivial tasks to check prior context."

// buildSystemPrompt returns the base prompt plus the
// current ISO date stamp and, if a goal service is
// passed and has an active goal, the [current_goal]
// block listing the title and pending tasks.
//
// F8: goal injection lives here so the main agent and
// any Darwin children see the same active goal.
func buildSystemPrompt(svc *goal.Service) string {
	base := supercliSystemPromptBase + "\n\n" + freshness.PromptSection(time.Now()) + "\n" + platformHint()
	if supercliCoordinatorMode {
		base += agent.CoordinatorPrompt()
	}
	if memoryBriefing != "" {
		base += "\n\n" + memoryBriefing
	}
	// Inject AFTER the briefing so the real sandbox root wins over
	// any conflicting project path a memory fact may carry.
	if workingDirNote != "" {
		base += "\n\n" + workingDirNote
	}
	base += "\n\n" + memoryAutoSaveInstruction
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

func envTruthy(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on", "y":
		return true
	default:
		return false
	}
}

func envFalsey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "0", "false", "no", "off", "n":
		return true
	default:
		return false
	}
}

// defaultStableToolset is the built-in default for the stable-toolset
// KV-cache optimisation (agent.LoopConfig.StableToolset): when true,
// tools activated via tool_search are not promoted into the request
// `tools` list, so the list stays byte-identical all session and the
// local server's prompt cache survives activations. OFF until a live
// test with a local model confirms tail tools are still called
// correctly from the tool_search result text alone; flip to true (or
// set `stable_toolset = true` in config.toml) after that test.
const defaultStableToolset = false

// resolveStableToolset applies the config.toml tri-state override
// (`stable_toolset`) on top of the built-in default.
func resolveStableToolset(override *bool) bool {
	if override != nil {
		return *override
	}
	return defaultStableToolset
}

func Main() {
	startupT := time.Now()
	// ABSOLUTE FIRST thing: catch ANY panic and log it.
	// If the program crashes silently, check supercli-data/logs/crash.log
	// next to the executable. `dataDir` is captured by the closure:
	// until the data root is resolved we fall back to the portable
	// default; afterwards the crash log lands in the resolved data
	// dir, same as every other crash path (crash.go).
	var home string
	dataDir := storage.PortableDataRoot()
	defer func() {
		if r := recover(); r != nil {
			logCrash(dataDir, r)
			fmt.Fprintf(os.Stderr, "\nFATAL: %v\nCheck %s for stack trace.\n", r, crashLogPath(dataDir))
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
	providerFlag := flag.String("provider", "", "LLM provider: openai, anthropic, codex, opencode, or echo (default: openai if SUPERCLI_LLM_API_KEY set, else echo)")
	modelFlag := flag.String("model", "", "model id (default: env SUPERCLI_LLM_MODEL, then gpt-4o-mini)")
	keyFlag := flag.String("key", "", "API key (overrides SUPERCLI_LLM_API_KEY)")
	baseFlag := flag.String("base-url", "", "base URL (overrides SUPERCLI_LLM_BASE_URL)")
	echoFlag := flag.Bool("echo", false, "force echo provider regardless of env/flags")
	debugFlag := flag.Bool("debug", false, "verbose logging")
	maxSession := flag.Int64("max-credits-per-session", 0, "cap total tokens (in+out) per session (0 = no cap)")
	maxDay := flag.Int64("max-credits-per-day", 0, "cap total tokens (in+out) per UTC day (0 = no cap)")
	draftModeFlag := flag.String("draft-mode", "off", "F11 draft mode: off|always|balanced|critical (default off; opt-in, requires --draft-model)")
	draftModelFlag := flag.String("draft-model", "", "F11 draft model id (required to enable F11; no auto-pick)")
	configFlag := flag.String("config", "", "path to config.toml override")
	batchFlag := flag.String("batch", "", "F33: run prompt without TUI, output to stdout and exit")
	resumeFlag := flag.Bool("resume", false, "resume the most recent session on startup")
	coordinatorFlag := flag.Bool("coordinator", false, "run main loop as a lightweight coordinator that delegates code work to isolated task workers (default on)")
	noCoordinatorFlag := flag.Bool("no-coordinator", false, "disable default coordinator mode and expose the normal tool set directly to the main loop")
	unsandboxedFlag := flag.Bool("allow-all", false, "grant full filesystem access — file operations can reach any directory (sensitive system paths still blocked); same as allow_all = true in config.toml")
	flag.Usage = usage
	flag.Parse()
	supercliCoordinatorMode = true
	if *noCoordinatorFlag || envFalsey("SUPERCLI_COORDINATOR") {
		supercliCoordinatorMode = false
	}
	if *coordinatorFlag || envTruthy("SUPERCLI_COORDINATOR") {
		supercliCoordinatorMode = true
	}

	if *showVersion {
		fmt.Println("supercli", version)
		return
	}

	resolvedHome, err := storage.ResolveHome(*homeFlag)
	if err != nil {
		fatal("resolve home", err)
	}
	home = resolvedHome
	// workingDirNote is set below, after the TOML + unsandboxed
	// flag are resolved so it reflects the real sandbox state.

	// SuperCli is ALWAYS portable: the single data directory holds
	// every piece of CLI state and lives next to the executable
	// (supercli-data/), unless --home/$SUPERCLI_HOME explicitly
	// override it.
	resolvedData, portable, err := storage.ResolveDataRoot(*homeFlag)
	if err != nil {
		fatal("resolve data dir", err)
	}
	dataDir = resolvedData

	// One-time migration from the legacy ~/.supercli location.
	if portable {
		if msg, merr := migrateLegacyData(dataDir); merr != nil {
			log.Printf("legacy data migration failed: %v (continuing with a fresh %s)", merr, dataDir)
		} else if msg != "" {
			fmt.Fprintln(os.Stderr, msg)
		}
	}

	if err := storage.EnsureDir(dataDir); err != nil {
		fatalUnwritableDataDir(dataDir, portable, err)
	}
	// Verify write permissions — the exe may sit in a read-only
	// location (e.g. Program Files, network drives).
	if err := checkDirWritable(dataDir); err != nil {
		fatalUnwritableDataDir(dataDir, portable, err)
	}

	// F29: resolve config.toml hierarchy.
	// global < project < --config < env < flags.
	cwd, _ := os.Getwd()
	tomlCfg, tomlErr := config.ResolveConfig(dataDir, cwd, *configFlag)
	if tomlErr != nil {
		log.Printf("config.toml: %v (using defaults)", tomlErr)
	}
	// Apply TOML as defaults (env/flags still win later).
	config.TomlConfigToEnv(tomlCfg)
	// Unsandboxed: flag > env (which TomlConfigToEnv may have set) > default off.
	if *unsandboxedFlag || envTruthy("SUPERCLI_ALLOW_ALL") {
		sandbox.Unsandboxed = true
	}
	// State the real sandbox root (the BaseDir file tools enforce) so
	// the model's first file/list call uses the correct path. Set AFTER
	// the unsandboxed decision so it reflects the actual sandbox state.
	if sandbox.Unsandboxed {
		workingDirNote = "Working directory: " + home +
			"\nFull filesystem access is ON (--allow-all). You can read and write files anywhere on the filesystem. Prefer absolute paths."
	} else {
		workingDirNote = "Working directory (file sandbox root): " + home +
			"\nUse this exact path for file and directory operations. Relative paths resolve here; paths must stay inside it."
	}
	initCodexAuth(dataDir, tomlCfg)
	appLog := initAppLog(dataDir)
	if appLog != nil {
		defer appLog.Close()
	}

	db, err := storage.OpenAt(dataDir)
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
		skills := discoverSkillsForDoctor(home, dataDir)
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
		runBatch(*batchFlag, home, dataDir, *providerFlag, *keyFlag, *baseFlag, *modelFlag, *echoFlag, *debugFlag, *draftModeFlag, *draftModelFlag)
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
	// Reasoning effort (OpenAI reasoning models): restore the
	// persisted level; /reasoning changes it at runtime.
	if tomlCfg.ReasoningEffort != "" {
		if err := llm.SetReasoningEffort(tomlCfg.ReasoningEffort); err != nil {
			log.Printf("config: reasoning_effort: %v (ignored)", err)
		}
	}
	// Apply draft/credit overrides from TOML if not set by flags.
	if *draftModeFlag == "off" && tomlCfg.DraftMode != "" {
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

	// Wave 4: first-run onboarding. When nothing at all is
	// configured (no providers in config.toml, no env/flag
	// provider — the resolved provider fell back to echo
	// without the user asking for it), walk the user through
	// a minimal setup, persist config.toml, and continue into
	// chat with the chosen provider.
	if !*echoFlag && cfg.IsEcho() &&
		len(tomlCfg.Providers) == 0 && tomlCfg.Provider == "" && tomlCfg.DefaultProvider == "" {
		if res := tui.RunOnboarding(); !res.Skipped {
			// "Sign in with ChatGPT" needs the OAuth browser flow,
			// which the wizard cannot run itself. Do it here, on
			// the plain console, before the TUI starts.
			if res.AuthMethod == tui.AuthChatGPT {
				initCodexAuth(dataDir, tomlCfg)
				if _, err := codexAuthMgr.Login(context.Background(), os.Stdout); err != nil {
					fmt.Fprintf(os.Stderr, "ChatGPT login failed: %v\nFalling back to setup-free start — run /login inside SuperCli to retry.\n", err)
				} else {
					res.BaseURL = codexAuthMgr.Options().BackendURL
				}
			}
			globalTomlPath, _ := config.FindTomlPaths(dataDir, cwd)
			saved := tomlCfg
			saved.Providers = []config.ProviderConf{{
				Name:    res.Name,
				Type:    res.Type,
				BaseURL: res.BaseURL,
				APIKey:  res.APIKey,
				Model:   res.Model,
			}}
			saved.DefaultProvider = res.Name
			if res.Model != "" {
				saved.DefaultModel = res.Model
			}
			if err := config.SaveToml(globalTomlPath, saved); err != nil {
				log.Printf("onboarding: save config.toml: %v", err)
			}
			tomlCfg = saved
			cfg.Provider = res.Type
			cfg.BaseURL = res.BaseURL
			cfg.APIKey = res.APIKey
			if res.Model != "" {
				cfg.Model = res.Model
			}
			if err := cfg.Normalize(); err != nil {
				log.Printf("onboarding: normalize config: %v", err)
			}
		}
	}

	// F16: build the capability registry from
	// seed + catalog + probe cache. A load
	// failure is logged and we fall back to an
	// empty registry — never a hardcoded table
	// (the F16 cardinal rule: the registry is
	// the only place we look up capabilities).
	caps, err := llm.NewCapabilityRegistryFromSources(dataDir, db)
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

	provider, err := buildProvider(cfg, dataDir, caps)
	if err != nil {
		fatal("init provider", err)
	}
	// If we started on a Codex model, refresh the usage snapshot in the
	// background so the HUD `limit:` tile shows current numbers right
	// away (not just the last on-disk snapshot). Async + silent — never
	// blocks startup. No redraw notifier here: the TUI program does not
	// exist yet, and the on-disk snapshot already renders on the first
	// frame; the swap path below wires the redraw that the bug needs.
	kickCodexUsageRefresh(provider, nil)

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
	registry.MustRegister(tools.NewListDir(home).Spec())

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

	discoverer := tools.NewDiscoverer(home, dataDir)
	skillApplier := tools.NewSkillApplier(discoverer)
	registry.MustRegister(skillApplier.Spec())

	userLoader := tools.NewUserToolLoader(home, dataDir)
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
	audit, auditErr := credits.NewAudit(dataDir)
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
	if supercliCoordinatorMode {
		registry.MarkAlwaysOn("ask_user")
		registry.MarkAlwaysOn("tool_search")
		registry.MarkAlwaysOn("goal")
	} else if smallTier {
		for _, name := range []string{
			"read_lines", "read_context", "edit_line", "edit_lines",
			"insert_after", "delete_lines", "write_file", "make_dir",
			"move", "copy", "trash",
			"list_dir",
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

	// Wave 2 memory: two SQLite stores. The GLOBAL store lives in
	// <data dir>/memory.db (user preferences + one "card" per known
	// project); the PROJECT store lives in
	// <data dir>/projects/<name>-<hash>/memory.db (facts,
	// decisions, session log). <data dir>/projects.json maps
	// project paths to their directories. All failures are
	// non-fatal: memory never blocks startup.
	memoryHome := dataDir
	globalMemStore, gMemErr := memory.OpenStore(memoryHome)
	if gMemErr != nil {
		log.Printf("global memory store: %v (global memory disabled)", gMemErr)
		globalMemStore = nil
	} else {
		defer globalMemStore.Close()
	}
	memStore, memErr := memory.OpenProjectStore(memoryHome, home)
	if memErr != nil {
		log.Printf("project memory store: %v (F5 disabled)", memErr)
		memStore = nil
	} else {
		defer memStore.Close()
	}
	// Embedding backend detection pings local servers, so it runs
	// in the background; until it lands, searches are FTS5-only.
	go func() {
		defer recoverAndLog(dataDir)()
		if e := memory.DetectEmbedder(cfg.APIKey); e != nil {
			globalMemStore.SetEmbedder(e)
			memStore.SetEmbedder(e)
		}
	}()
	// Refresh this project's card (bumps last-session) and build
	// the session-start briefing injected into the system prompt.
	memory.RefreshCard(globalMemStore, home, "", "active")
	briefCap := 700
	if smallTier {
		briefCap = 300
	}
	memoryBriefing = memory.BuildBriefing(globalMemStore, memStore, home, briefCap)
	memAutoSaver := &memory.AutoSaver{Project: memStore, Global: globalMemStore, ProjectPath: home}
	// memProg tracks how much of the conversation the incremental
	// background saver already summarized (see incrementalMemorySave).
	memProg := &memProgress{}

	// Persistent memory tools: always-on so the model can save
	// and recall facts across sessions. remember routes entries
	// to the project or global store via its `scope` argument;
	// recall searches both hybridly (FTS5 + vectors when an
	// embedder was detected).
	if memStore != nil || globalMemStore != nil {
		rememberTool := tools.NewRememberDual(storeOrNil(memStore), storeOrNil(globalMemStore))
		rememberTool.OnSave = memAutoSaver.NoteRemember
		registry.MustRegister(rememberTool.Spec())
		registry.MustRegister(tools.NewRecallDual(storeOrNil(memStore), storeOrNil(globalMemStore)).Spec())
		registry.MarkAlwaysOn("remember")
		registry.MarkAlwaysOn("recall")
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

	var injector *reflect.Injector
	if memStore != nil {
		patStore, _ := reflect.NewStore(memStore)
		if patStore != nil {
			ext := &reflect.Extractor{
				ErrorsPath:  filepath.Join(logsDir, "tool_errors.log"),
				MaxPatterns: 5,
			}
			// Pattern extraction parses the whole tool_errors.log;
			// run it off the startup path (the injector reads the
			// store lazily, so late-stored patterns still apply).
			go func() {
				defer recoverAndLog(dataDir)()
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
			}()
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

	// F28: external prices (pricepertoken.com, OpenRouter) push
	// fetched rates into the credits package so CostFor/StatusBar
	// use live prices. Non-fatal: if all sources fail, the
	// hardcoded fallback rates in credits/cost.go still work.
	//
	// Startup-latency rule: NEVER hit the network on the startup
	// path. Apply the 24h disk cache synchronously (pure file
	// read); only when it's missing/stale, fetch in the
	// background — rates pop in a second or two after the TUI is
	// already interactive.
	if cachedPrices := pricing.LoadCache(dataDir); len(cachedPrices) > 0 {
		pricing.ApplyCachedRates(dataDir)
		applyPricingMetadata(caps, cachedPrices)
		if !pricing.HasContextMetadata(cachedPrices) {
			fetcher := pricing.NewFetcher(dataDir)
			capsSnapshot := caps.All()
			go func() {
				defer recoverAndLog(dataDir)()
				updated := fetcher.FetchAndUpdate(capsSnapshot)
				applyModelInfoMetadata(caps, updated)
			}()
		}
	} else {
		fetcher := pricing.NewFetcher(dataDir)
		capsSnapshot := caps.All()
		go func() {
			defer recoverAndLog(dataDir)()
			updated := fetcher.FetchAndUpdate(capsSnapshot)
			applyModelInfoMetadata(caps, updated)
		}()
	}

	// F13: open the session store. Messages get persisted
	// as the loop emits them, and a FTS5 index on
	// messages.content keeps the search_history tool fast.
	// A failure here is non-fatal: search_history is
	// disabled, but the loop still runs in-memory.
	sessStore, sessErr := session.OpenStore(dataDir)
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
		draftSink = reflect.NewJSONLDraftOverrideSink(filepath.Join(dataDir, "reflect"))
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

	// Wave 4: context-window resolution + auto-compact wiring.
	// Cascade: config context_window > provider /v1/models
	// metadata (fetched in the background, parsed defensively)
	// > learned limit (persisted from past context-length
	// errors) > 16384 default (applied inside the loop).
	learned := loadLearnedLimits(dataDir)
	var provWinMu sync.Mutex
	provWindows := map[string]int{}
	if cfg.BaseURL != "" {
		go func() {
			defer recoverAndLog(dataDir)()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			m, err := llm.ListProviderModelContexts(ctx, cfg.BaseURL, cfg.APIKey)
			if err != nil || len(m) == 0 {
				return
			}
			provWinMu.Lock()
			provWindows = m
			provWinMu.Unlock()
		}()
	}
	windowFor := func(model string) int {
		if tomlCfg.ContextWindow > 0 {
			return tomlCfg.ContextWindow
		}
		provWinMu.Lock()
		w := provWindows[model]
		provWinMu.Unlock()
		if w > 0 {
			return w
		}
		if info, ok := caps.Get(model); ok && info.ContextLength > 0 {
			return info.ContextLength
		}
		if v := learned.Get(model); v > 0 {
			return v
		}
		return 0 // loop falls back to its 16384 default
	}
	autoSummarizer := func(ctx context.Context, p llm.Provider, msgs []llm.Message) (string, error) {
		summary, err := summarizeForCompaction(ctx, p, msgs)
		if err != nil {
			return "", err
		}
		return wrapCompactSummary(summary), nil
	}

	// Build the real loop. Pass the home as the image base dir.
	loop, err := agent.NewLoop(agent.LoopConfig{
		Provider:        provider,
		Registry:        registry,
		System:          buildSystemPrompt(goalSvc),
		Briefing:        memoryBriefing,
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
		WindowFor:         windowFor,
		Summarizer:        autoSummarizer,
		LearnLimit:        learned.Learn,
		EnableNavigator:   true,
		// Thin tool protocol: small-tier models get the compact
		// catalog + full schemas only for the core; they suffer most
		// from schema bulk in the prefill. Big models keep native
		// JSON tool calling with full schemas. Mirrors the same
		// smallTier gate that already trims their always-on set.
		ThinTools: smallTier,
		// Stable toolset: keep the request `tools` list fixed all
		// session so tool_search activations don't invalidate the
		// server-side KV prompt cache. `stable_toolset = true|false`
		// in config.toml overrides the built-in default.
		StableToolset: resolveStableToolset(tomlCfg.StableToolset),
		BaseDir:       home,
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
	sendMessageTool := agent.NewSendMessageTool(at.Workers)
	registry.MustRegister(sendMessageTool.Spec())
	taskStopTool := agent.NewTaskStopTool(at.Workers)
	registry.MustRegister(taskStopTool.Spec())
	if supercliCoordinatorMode {
		registry.MarkAlwaysOn("task")
		registry.MarkAlwaysOn("send_message")
		registry.MarkAlwaysOn("task_stop")
	}

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

	// Fala 3: /workers — coordinator visibility. Lists workers from the
	// task registry; "/workers stop <id>" cancels a running one.
	mergedCommands["workers"] = func(ctx context.Context, args string) (string, error) {
		fields := strings.Fields(args)
		if len(fields) >= 1 && strings.EqualFold(fields[0], "stop") {
			if len(fields) < 2 {
				return "usage: /workers stop <id>   (id like worker-1; see /workers)", nil
			}
			id := fields[1]
			if !strings.HasPrefix(id, "worker-") {
				id = "worker-" + id
			}
			if err := at.Workers.Stop(id); err != nil {
				return fmt.Sprintf("workers: %v", err), nil
			}
			return fmt.Sprintf("workers: stop requested for %s", id), nil
		}
		return formatWorkers(at.Workers), nil
	}

	// Fala 3: /context — where the input tokens go (system prompt,
	// tool schemas, tool results, messages) plus the top 5 heaviest
	// items, so the user can see what is bloating the context.
	mergedCommands["context"] = func(ctx context.Context, args string) (string, error) {
		return agent.FormatContextReport(loop.ContextReport()), nil
	}

	// MCP client: spawn configured [mcp.servers.*] stdio servers in the
	// background, register their tools (deferred, tool_search-only),
	// and expose status/restart via /mcp.
	reindexTools := func() {
		if err := toolSearcher.RebuildIndex(); err != nil {
			log.Printf("mcp: tool index rebuild: %v", err)
		}
	}
	mcpManager := initMcp(tomlCfg, registry, reindexTools)
	if mcpManager != nil {
		defer mcpManager.StopAll()
	}
	mergedCommands["mcp"] = mcpCommand(mcpManager, registry, reindexTools)

	// /allow-all — toggle full filesystem access. Persists to config.toml.
	mergedCommands["allow-all"] = func(ctx context.Context, args string) (string, error) {
		switch strings.ToLower(strings.TrimSpace(args)) {
		case "on", "true", "1":
			sandbox.Unsandboxed = true
			workingDirNote = "Working directory: " + home +
				"\nFull filesystem access is ON (--allow-all). You can read and write files anywhere on the filesystem. Prefer absolute paths."
		case "off", "false", "0", "":
			sandbox.Unsandboxed = false
			workingDirNote = "Working directory (file sandbox root): " + home +
				"\nUse this exact path for file and directory operations. Relative paths resolve here; paths must stay inside it."
		default:
			return "usage: /allow-all on|off", nil
		}
		globalPath, _ := config.FindTomlPaths(dataDir, cwd)
		if tc, err := config.LoadToml(globalPath); err == nil {
			tc.AllowAll = sandbox.Unsandboxed
			if err := config.SaveToml(globalPath, tc); err != nil {
				log.Printf("allow-all: save config.toml: %v", err)
			}
		}
		if sandbox.Unsandboxed {
			return "Full filesystem access is now ON — file operations can reach any directory (sensitive system paths still blocked). Persisted to config.toml.", nil
		}
		return "Sandbox is now ON — file operations restricted to the working directory. Persisted to config.toml.", nil
	}

	mergedCommands["clear"] = func(ctx context.Context, args string) (string, error) {
		hidden := loop.HideLastUserTurns(2)
		if hidden == 0 {
			return "nothing to clear", nil
		}
		return fmt.Sprintf("cleared: hid %d message(s) from context (scrollback kept)", hidden), nil
	}

	// Wave 4: /resume — list recent sessions or load one back
	// into the live loop. The continuation is recorded under
	// the NEW session id (sessWriter keeps writing here); the
	// original session stays intact and searchable.
	mergedCommands["resume"] = func(ctx context.Context, args string) (string, error) {
		if sessStore == nil {
			return "resume: session store unavailable", nil
		}
		args = strings.TrimSpace(args)
		if args == "" {
			return listResumableSessions(ctx, sessStore, sessionID)
		}
		out, err := resumeSession(ctx, loop, sessStore, windowFor, args)
		if err != nil {
			return fmt.Sprintf("resume: %v", err), nil
		}
		return out, nil
	}
	// --resume: load the most recent prior session at startup.
	if *resumeFlag && sessStore != nil {
		if recent, err := sessStore.ListRecent(context.Background(), 2); err == nil {
			for _, r := range recent {
				if r.ID == sessionID {
					continue
				}
				if msg, err := resumeSession(context.Background(), loop, sessStore, windowFor, r.ID); err == nil {
					log.Printf("--resume: %s", msg)
				} else {
					log.Printf("--resume failed: %v", err)
				}
				break
			}
		}
	}

	// F12: the cross-model consultation block (consult tool +
	// /council slash command) is registered further down, after
	// the provider manager exists — the user-picked council
	// roster needs config.toml provider entries to build
	// per-model providers.

	// F25a: /help — list all registered slash commands.
	mergedCommands["help"] = func(ctx context.Context, args string) (string, error) {
		// Short grouped list by default; /help all shows everything.
		if strings.TrimSpace(strings.ToLower(args)) == "all" {
			return tui.HelpContentAll(), nil
		}
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

	// /reasoning — show or set the reasoning-effort level for
	// OpenAI-family reasoning models. Persisted to the global
	// config.toml; sent only to models that support the parameter.
	mergedCommands["reasoning"] = func(ctx context.Context, args string) (string, error) {
		args = strings.ToLower(strings.TrimSpace(args))
		modelName := loop.Provider().Name()
		if args == "" {
			cur, effective, adjusted := llm.ReasoningEffortAdjustment(modelName)
			if cur == "" {
				cur = "(not set — provider default)"
			}
			note := ""
			if !llm.SupportsReasoningEffort(modelName) {
				note = fmt.Sprintf("\nnote: current model %q does not support reasoning effort; the parameter is not sent", modelName)
			}
			if supported, ok := llm.SupportedReasoningEfforts(modelName); ok {
				note += fmt.Sprintf("\nbackend-supported for %s: %s", modelName, strings.Join(supported, "|"))
			}
			if adjusted {
				note += fmt.Sprintf("\neffective for current model: %s (configured %s was adjusted from backend evidence)", effective, cur)
			}
			return fmt.Sprintf("reasoning effort: %s\nusage: /reasoning <%s|off>%s",
				cur, strings.Join(llm.ReasoningEffortLevels, "|"), note), nil
		}
		if args == "off" || args == "default" {
			args = ""
		}
		if err := llm.SetReasoningEffort(args); err != nil {
			return fmt.Sprintf("reasoning: %v", err), nil
		}
		// Persist to the GLOBAL config.toml (same file the
		// onboarding wizard and provider manager write).
		globalPath, _ := config.FindTomlPaths(dataDir, cwd)
		if tc, err := config.LoadToml(globalPath); err == nil {
			tc.ReasoningEffort = args
			if err := config.SaveToml(globalPath, tc); err != nil {
				log.Printf("reasoning: save config.toml: %v", err)
			}
		}
		if args == "" {
			return "reasoning effort cleared (provider default)", nil
		}
		out := fmt.Sprintf("reasoning effort set to %s", args)
		if !llm.SupportsReasoningEffort(modelName) {
			out += fmt.Sprintf("\nnote: current model %q does not support it; the parameter will apply when you switch to an OpenAI reasoning model", modelName)
		} else if configured, effective, adjusted := llm.ReasoningEffortAdjustment(modelName); adjusted {
			out += fmt.Sprintf("\nnote: current backend evidence adjusts %s -> %s for %s", configured, effective, modelName)
		}
		return out, nil
	}

	// /usage — force a fresh fetch of the ChatGPT-subscription usage
	// limits (5h rolling + weekly window) from the dedicated usage
	// endpoint and print them. This is NOT a completion: it hits the
	// usage endpoint directly and does not consume the quota. When the
	// active provider is not Codex (or has no auth) it prints a clear
	// message instead of an error.
	mergedCommands["usage"] = func(ctx context.Context, args string) (string, error) {
		prov := loop.Provider()
		_, single := prov.(codexUsageFetcher)
		_, all := prov.(codexUsageAllFetcher)
		if !single && !all {
			return "the active model is not a ChatGPT-subscription (Codex) model — usage limits are only available there.\nRun /login and /model gpt-5.5 to switch.", nil
		}

		fctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		// Refresh BEFORE building the pool table so every account's
		// snapshot is current. For a multi-account router this fetches
		// ALL accounts (each with its own token), so the pool table and
		// POOL aggregate reflect every account — not just the active one.
		rl, err := refreshCodexUsage(fctx, prov)
		// The per-account pool table (multi-account router) is built
		// from each account's last-known snapshot AFTER the refresh, so
		// it shows the freshly-fetched numbers; it stays useful even
		// when some accounts failed to refresh (expired token, offline).
		pool := codexPoolUsageDetail(prov)
		// Partial success (multi-account): the active account refreshed
		// fine but another account's token failed. Don't treat that as a
		// total failure — show the fresh active numbers and pool table,
		// noting which account(s) could not refresh.
		if err != nil && rl.OK {
			return fmt.Sprintf("Codex usage (just refreshed; some accounts failed: %v):\n%s%s",
				err, rl.FormatDetail(), pool), nil
		}
		if err != nil {
			if rp, ok := prov.(interface {
				RateLimits() (llm.CodexRateLimits, bool)
			}); ok {
				if cached, ok := rp.RateLimits(); ok {
					return "could not refresh (showing last known):\n" + cached.FormatDetail() + pool, nil
				}
			}
			// No snapshot for the active account, but the pool may
			// still have per-account data worth showing. Either way the
			// real reason (HTTP status, body, URL) MUST be surfaced —
			// dropping err here is what made /usage print a bare
			// "could not refresh the active account:" with nothing after.
			if pool != "" {
				return fmt.Sprintf("could not refresh the active account: %v%s", err, pool), nil
			}
			return fmt.Sprintf("could not fetch Codex usage: %v", err), nil
		}
		return "Codex usage (just refreshed):\n" + rl.FormatDetail() + pool, nil
	}

	// Wave 2 B6: /memory — inspect persistent memory. No args:
	// overview (recent entries, DB sizes, embedding status).
	// `/memory search <q>` runs a hybrid search over both stores;
	// `/memory forget <id>` deletes an entry wherever it lives.
	mergedCommands["memory"] = func(ctx context.Context, args string) (string, error) {
		return memoryCommand(ctx, memStore, globalMemStore, memoryBriefing, args)
	}

	// /projects — manage the per-project memory map. Backed by
	// internal/storage/memory/projects.go; the slash command is a
	// thin shell that calls into app.projectsCommand. The
	// interactive TUI menu (opened from the 'p' shortcut or any
	// /projects invocation without args) lives in tui/menu_projects.go.
	mergedCommands["projects"] = func(ctx context.Context, args string) (string, error) {
		return projectsCommand(ctx, args, dataDir)
	}

	// Wave 1 cleanup: the old text-only /models handler was dead
	// code — the TUI rewrites /models to /model before handlers
	// run, so the interactive picker always won. Removed.

	// F30: create provider manager, load persisted
	// hidden-models state, and reload the providers list
	// from config.toml so model filtering works immediately.
	// (Created before /login so it can register the codex
	// provider entry.)
	provMgr := providers.NewManager(dataDir)
	// Persist the runtime /model selection to the highest-priority
	// config that startup resolution actually reads. When a project
	// config (<cwd>/.supercli/config.toml) is in effect it overrides
	// the global config at startup, so saving the selection only to
	// the global config would let the project config silently shadow
	// it — the model swap would be forgotten on the next launch.
	if gp, pp := config.FindTomlPaths(dataDir, cwd); pp != "" {
		if _, statErr := os.Stat(pp); statErr == nil {
			provMgr.SetActiveConfigPath(pp)
		} else {
			provMgr.SetActiveConfigPath(gp)
		}
	}
	provMgr.Reload()
	provMgr.LoadHiddenState()

	// ChatGPT-OAuth (codex) providers have no /v1/models endpoint,
	// so the background ScanModels alone could never discover their
	// models in past releases. Register the static Codex catalog for
	// every configured codex-type entry NOW, under the entry's own
	// name — otherwise the /model picker stays empty after a restart
	// (the catalog used to be registered only inside the /login
	// handler, and only under the hardcoded "codex" name, while the
	// onboarding wizard saves the entry as name "openai").
	for _, p := range provMgr.Configured() {
		if p.Type == config.ProviderCodex {
			llm.RegisterCodexCatalog(caps, p.Name)
		}
	}

	// F12: cross-model consultation. Built here (after the
	// provider manager) so the user-picked council roster can
	// resolve "providerName/modelID" specs against config.toml
	// provider entries (local + online endpoints alike).
	//
	// buildCouncilMember builds a one-shot provider for a
	// roster spec. The spec is "providerName/modelID" when the
	// prefix matches a configured provider (model ids may
	// themselves contain "/" or ":", e.g. openrouter/ollama);
	// otherwise the whole spec is treated as a bare model id
	// served by the active transport.
	buildCouncilMember := func(spec string) (llm.Provider, error) {
		provName, model := "", spec
		if i := strings.Index(spec, "/"); i > 0 {
			prefix := spec[:i]
			for _, pc := range provMgr.Configured() {
				if pc.Name == prefix {
					provName, model = prefix, spec[i+1:]
					break
				}
			}
		}
		if provName == "" {
			provName = caps.Provider(model)
		}
		mCfg := cfg
		mCfg.Model = model
		for _, pc := range provMgr.Configured() {
			if pc.Name == provName {
				mCfg.Provider = pc.Type
				mCfg.BaseURL = pc.BaseURL
				mCfg.APIKey = pc.APIKey
				break
			}
		}
		return buildProvider(mCfg, dataDir, caps)
	}

	// The auto council (cheapest-N pool) stays as the fallback
	// for the consult tool and for /council when the user never
	// picked a roster.
	council := buildConsultCouncil(3, provider, caps, cfg)
	if council == nil {
		// No cheap pool available — keep a judge-only council
		// so explicit model selection (tool `models` param and
		// the /council roster) still works.
		council = &consult.Council{Judge: provider}
	}
	consultTool := tools.NewConsult(council)
	consultTool.BuildProvider = buildCouncilMember
	consultTool.OnResult = func(r consult.Result) {
		winner := r.Verdict.WinnerIndex
		prov := ""
		if !r.AllFailed && winner >= 0 && winner < len(r.Candidates) {
			prov = r.Candidates[winner].Provider
		} else {
			winner = -1
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

	// /council — manual brainstorming across hand-picked models.
	//
	//	/council               → multi-select roster picker
	//	                         (space toggles, enter confirms;
	//	                          selection persists in config.toml
	//	                          under [council] models)
	//	/council <question>    → ask the saved roster in parallel
	//	/council N <question>  → legacy: auto cheapest-N pool
	//	                         (used only when no roster is saved)
	mergedCommands["council"] = func(ctx context.Context, args string) (string, error) {
		q := strings.TrimSpace(args)
		if q == "" {
			// Interactive roster picker via the ask_user UI.
			opts := councilPickerOptions(provMgr, caps)
			if len(opts) == 0 {
				return "council: no models available — add providers via /providers first", nil
			}
			respond := make(chan tools.AskAnswer, 1)
			req := tools.AskRequest{
				ID:          "council-pick",
				Question:    "Pick council members (space toggles, enter confirms)",
				Header:      "council",
				Options:     opts,
				MultiSelect: true,
				Respond:     respond,
			}
			select {
			case askCh <- req:
			case <-ctx.Done():
				return "", ctx.Err()
			}
			select {
			case ans := <-respond:
				if ans.Cancelled || len(ans.Selected) == 0 {
					return "council: selection cancelled", nil
				}
				if err := provMgr.SaveCouncilModels(ans.Selected); err != nil {
					log.Printf("council: save roster failed: %v", err)
				}
				return fmt.Sprintf("council roster saved: %s\nask away: /council <question>",
					strings.Join(ans.Selected, ", ")), nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		// Roster: last picker selection (global config.toml) or
		// the merged [council] models section (project override).
		roster := provMgr.LoadCouncilModels()
		if len(roster) == 0 {
			roster = tomlCfg.Council.Models
		}
		// Legacy optional leading N (only meaningful for the
		// auto-pool fallback path).
		n := 3
		if fields := strings.Fields(q); len(fields) > 1 {
			if v, err := strconv.Atoi(fields[0]); err == nil && v > 0 {
				n = v
				q = strings.TrimSpace(strings.TrimPrefix(q, fields[0]))
			}
		}
		if len(roster) == 0 {
			// Fallback: auto cheapest-N council with judge pick.
			if len(council.Samples) == 0 {
				return "council: no roster picked yet — run /council (no args) to choose models", nil
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
		// Hand-picked roster: build each member; a single bad
		// model never aborts the rest.
		var provs []llm.Provider
		var specs []string
		var buildErrs []string
		for _, s := range roster {
			p, err := buildCouncilMember(s)
			if err != nil {
				buildErrs = append(buildErrs, fmt.Sprintf("model %s: error: %v", s, err))
				continue
			}
			provs = append(provs, p)
			specs = append(specs, s)
		}
		if len(provs) == 0 {
			return "council: no usable models in roster: " + strings.Join(buildErrs, "; "), nil
		}
		cc := &consult.Council{Samples: provs, Judge: loop.Provider()}
		res, err := cc.ConsultSelected(ctx, q, provs)
		if err != nil {
			return fmt.Sprintf("council: %v", err), nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "council × %d — %s\n", len(provs), q)
		for i, cd := range res.Candidates {
			if cd.Err != nil {
				fmt.Fprintf(&b, "\n━━ %s · error ━━\nmodel %s: error: %v\n", specs[i], specs[i], cd.Err)
				continue
			}
			fmt.Fprintf(&b, "\n━━ %s · %s · in %d / out %d tok ━━\n%s\n",
				specs[i], cd.Elapsed.Round(time.Millisecond), cd.In, cd.Out,
				strings.TrimSpace(cd.Response))
		}
		for _, e := range buildErrs {
			b.WriteString("\n" + e + "\n")
		}
		if w := res.Verdict.WinnerIndex; w >= 0 && w < len(specs) {
			fmt.Fprintf(&b, "\njudge (%s): winner=%s · %s\n", loop.Provider().Name(), specs[w], res.Verdict.Reason)
		} else if res.Verdict.Reason != "" {
			fmt.Fprintf(&b, "\njudge: %s\n", res.Verdict.Reason)
		}
		fmt.Fprintf(&b, "[%d model(s), %d total tokens]", len(res.Candidates), res.TotalTokens)
		return b.String(), nil
	}

	// ChatGPT-subscription auth: /login runs the OAuth+PKCE
	// browser flow and registers a "codex" provider entry;
	// /logout clears the saved tokens.
	mergedCommands["login"] = func(ctx context.Context, args string) (string, error) {
		if codexAuthMgr == nil {
			initCodexAuth(dataDir, tomlCfg)
		}
		// Multi-account: "/login <label>" signs a SECOND (named)
		// account into auth-<label>.json. Bare "/login" uses the
		// default account exactly as before.
		label := strings.TrimSpace(args)
		mgr := codexAuthMgr
		if label != "" {
			mgr = codexauth.NewManagerFor(dataDir, label, codexauth.Options{
				ClientID:   tomlCfg.CodexAuth.ClientID,
				Issuer:     tomlCfg.CodexAuth.Issuer,
				BackendURL: tomlCfg.CodexAuth.BackendURL,
			})
		}
		var status strings.Builder
		res, err := mgr.Login(ctx, &status)
		if err != nil {
			out := strings.TrimSpace(status.String())
			if out != "" {
				return out + "\n" + fmt.Sprintf("login failed: %v", err), nil
			}
			return fmt.Sprintf("login failed: %v", err), nil
		}
		// Register a "codex" provider entry so /model and the
		// provider menus can route through the ChatGPT backend.
		if provMgr != nil {
			if err := provMgr.Add("codex", config.ProviderCodex,
				mgr.Options().BackendURL, "", "gpt-5.5"); err != nil &&
				!strings.Contains(err.Error(), "already exists") {
				log.Printf("login: register codex provider: %v", err)
			}
			provMgr.Reload()
		}
		// Register the Codex model family in the capability
		// registry so /model gpt-5.5 resolves immediately
		// (the ChatGPT backend has no /v1/models to probe).
		llm.RegisterCodexCatalog(caps, "codex")
		plan := res.PlanType
		if plan == "" {
			plan = "unknown plan"
		}
		return fmt.Sprintf("logged in with ChatGPT (%s).\nUse /model to switch to a Codex model (e.g. gpt-5.5) — requests now route through the ChatGPT backend.", plan), nil
	}
	mergedCommands["logout"] = func(ctx context.Context, args string) (string, error) {
		// Multi-account: "/logout <label>" removes that named
		// account's auth-<label>.json. Bare "/logout" removes the
		// default account (and its usage snapshot) as before.
		label := strings.TrimSpace(args)
		if label != "" {
			mgr := codexauth.NewManagerFor(dataDir, label, codexauth.Options{})
			if !mgr.LoggedIn() {
				return fmt.Sprintf("account %s is not logged in", strconv.Quote(label)), nil
			}
			if err := mgr.Logout(); err != nil {
				return "", fmt.Errorf("logout %s: %w", label, err)
			}
			return fmt.Sprintf("logged out account %s (credentials removed)", strconv.Quote(label)), nil
		}
		if codexAuthMgr == nil || !codexAuthMgr.LoggedIn() {
			return "not logged in (no ChatGPT credentials saved)", nil
		}
		if err := codexAuthMgr.Logout(); err != nil {
			return "", fmt.Errorf("logout: %w", err)
		}
		// Drop the saved usage snapshot too, so the HUD does not keep
		// showing the logged-out account's rate limits.
		if err := llm.ClearCodexRateLimits(dataDir); err != nil {
			log.Printf("logout: clear usage snapshot: %v", err)
		}
		return "logged out — ChatGPT credentials and saved usage limits removed from the data dir", nil
	}

	// /accounts lists all logged-in ChatGPT accounts (default +
	// any named ones). With 2+, requests round-robin across them.
	mergedCommands["accounts"] = func(ctx context.Context, args string) (string, error) {
		labels, err := codexauth.ListAccounts(dataDir)
		if err != nil {
			return "", fmt.Errorf("accounts: %w", err)
		}
		var loggedIn []string
		for _, label := range labels {
			mgr := codexauth.NewManagerFor(dataDir, label, codexauth.Options{})
			if mgr.LoggedIn() {
				loggedIn = append(loggedIn, label)
			}
		}
		if len(loggedIn) == 0 {
			return "no ChatGPT accounts logged in. Use /login (or /login <label>) to add one.", nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "ChatGPT accounts (%d):\n", len(loggedIn))
		for _, label := range loggedIn {
			fmt.Fprintf(&b, "  - %s\n", label)
		}
		if len(loggedIn) > 1 {
			b.WriteString("requests round-robin across these accounts.")
		} else {
			b.WriteString("add another with /login <label> to enable round-robin.")
		}
		return strings.TrimRight(b.String(), "\n"), nil
	}

	// /account — show the current ChatGPT-subscription login (account
	// id + plan) without hitting the network. Read-only counterpart to
	// /login and /logout.
	mergedCommands["account"] = func(ctx context.Context, args string) (string, error) {
		if codexAuthMgr == nil {
			initCodexAuth(dataDir, tomlCfg)
		}
		info, err := codexAuthMgr.Account()
		if err != nil {
			return "", fmt.Errorf("account: %w", err)
		}
		if !info.LoggedIn {
			return "not logged in — run /login to sign in with ChatGPT.", nil
		}
		plan := info.PlanType
		if plan == "" {
			plan = "unknown"
		}
		acct := info.AccountID
		if acct == "" {
			acct = "(unknown)"
		}
		var b strings.Builder
		b.WriteString("ChatGPT account\n")
		b.WriteString(fmt.Sprintf("  plan:    %s\n", plan))
		b.WriteString(fmt.Sprintf("  account: %s", acct))
		if !info.LastRefresh.IsZero() {
			b.WriteString(fmt.Sprintf("\n  refreshed: %s", info.LastRefresh.Format("2006-01-02 15:04")))
		}
		return b.String(), nil
	}

	// F25a: /sandbox — show sandbox status.
	mergedCommands["sandbox"] = func(ctx context.Context, args string) (string, error) {
		status := "restricted"
		allowHint := ""
		if sandbox.Unsandboxed {
			status = "allow-all (full filesystem access)"
			allowHint = "\nuse /allow-all off to re-enable the sandbox"
		} else {
			allowHint = "\nuse /allow-all on for full filesystem access"
		}
		return fmt.Sprintf("sandbox: %s\nhome: %s\ndata: %s%s", status, home, dataDir, allowHint), nil
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
	registry.MustRegister(tools.NewEditLines(home).Spec())
	registry.MustRegister(tools.NewInsertAfter(home).Spec())
	registry.MustRegister(tools.NewDeleteLines(home).Spec())
	registry.MustRegister(tools.NewWriteFile(home).Spec())
	registry.MustRegister(tools.NewMakeDir(home).Spec())
	registry.MustRegister(tools.NewMove(home).Spec())
	registry.MustRegister(tools.NewCopy(home).Spec())
	registry.MustRegister(tools.NewTrash(home).Spec())

	// Web tools: web_fetch (SSRF-guarded HTML→text fetcher) and
	// web_search (DuckDuckGo by default — no key; Brave/Tavily
	// when [web_search] in config.toml or BRAVE_API_KEY /
	// TAVILY_API_KEY supplies a key). Both are opt-in (NOT
	// MarkAlwaysOn); the model discovers them via tool_search.
	registry.MustRegister(tools.NewWebFetch().Spec())
	wsEngine := tomlCfg.WebSearch.Engine
	wsKey := tomlCfg.WebSearch.APIKey
	if wsKey == "" {
		switch strings.ToLower(wsEngine) {
		case "brave":
			wsKey = os.Getenv("BRAVE_API_KEY")
		case "tavily":
			wsKey = os.Getenv("TAVILY_API_KEY")
		}
	}
	registry.MustRegister(tools.NewWebSearch(wsEngine, wsKey).Spec())

	// outlook_mail: Windows-only COM automation of desktop
	// Outlook (read folders/messages, search, create DRAFTS —
	// never sends/deletes/moves). Opt-in via tool_search; on
	// non-Windows it returns an explanatory error.
	registry.MustRegister(tools.NewOutlookMail().Spec())

	// Re-index for tool_search: many tools (ctx_execute, goal,
	// memory, task, consult, file-line tools, web tools, ...)
	// are registered AFTER the first RebuildIndex call above, so
	// without this second pass they were invisible to tool_search
	// and effectively unreachable for small-tier models.
	if err := toolSearcher.RebuildIndex(); err != nil {
		log.Printf("tool_search reindex: %v", err)
	}

	// F7 + F8 status bar. The goal line is rendered
	// above the credits line when both are present.
	statusFn := func() string {
		goal := goalSvc.StatusLine(context.Background())
		activeProvider := loop.Provider()
		activeModel := activeProvider.Name()
		cred := credits.StatusLine(tracker, activeModel)
		// F34: live token counter and cost projection.
		tokens := ""
		costStr := ""
		if draftStats != nil {
			turns := draftStats.Snapshot()
			total := stats.Sum(turns)
			totalTokens := total.TokensIn + total.TokensOut
			if totalTokens > 0 {
				tokens = compactNum(totalTokens)
				// Calculate cost from current model rates, including per-provider
				// OpenRouter/proxy prices when the capability registry knows which
				// configured provider owns the active model.
				if !isSubscriptionRuntimeProvider(activeProvider) {
					r, _ := credits.RateForProvider(providerNameForModel(caps, activeModel), activeModel)
					inputCost := float64(total.TokensIn) / 1000.0 * r.InputPer1k
					outputCost := float64(total.TokensOut) / 1000.0 * r.OutputPer1k
					totalCost := inputCost + outputCost
					if totalCost > 0 {
						costStr = fmt.Sprintf("$%.4f", totalCost)
					}
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
		// Reasoning effort badge, next to the model name, only
		// when set and applicable to the active model.
		if configured, effective, adjusted := llm.ReasoningEffortAdjustment(loop.Provider().Name()); effective != "" {
			if adjusted {
				bottom = append(bottom, "effort: "+configured+"→"+effective)
			} else {
				bottom = append(bottom, "effort: "+effective)
			}
		}
		if tokens != "" {
			tokStr := tokens
			if costStr != "" {
				tokStr += " │ " + costStr
			}
			bottom = append(bottom, "tok: "+tokStr)
		}
		// Codex subscription usage (5h rolling + weekly), pulled from
		// the active provider's last /responses headers. Rendered only
		// when the active provider is Codex AND a snapshot has arrived.
		if rp, ok := loop.Provider().(interface {
			RateLimits() (llm.CodexRateLimits, bool)
		}); ok {
			if rl, ok := rp.RateLimits(); ok {
				if hud := rl.FormatHUD(); hud != "" {
					tile := "limit: " + hud
					// Multi-account: append which account is active
					// AND the pool-wide average, so the user sees both
					// "this account" and "all accounts combined".
					if rt, ok := loop.Provider().(*llm.RouterProvider); ok {
						snaps, _, active := rt.PoolUsage()
						if len(snaps) > 1 {
							tile += fmt.Sprintf(" · acct: %s (%d/%d)", rt.ActiveLabel(), active+1, len(snaps))
							if p5, p7, n := rt.PoolAggregate(); n > 0 {
								tile += fmt.Sprintf(" · pool %dacct 5h ~%d%% 7d ~%d%%", n, p5, p7)
							}
						}
					}
					bottom = append(bottom, tile)
				}
			}
		}
		// Fala 3: inline worker visibility. Show a compact tile
		// ("2 running · 1 done") whenever the coordinator has spawned
		// workers, so the user sees activity without typing /workers.
		if at != nil && at.Workers != nil {
			if tile := at.Workers.Counts().StatusTile(); tile != "" {
				bottom = append(bottom, "workers: "+tile)
			}
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

	// Scan all configured providers for models in the
	// background so they're ready when the user opens
	// /models or /model. Non-blocking — the TUI starts
	// immediately, models appear as they're discovered.
	go provMgr.ScanModels(caps)

	// Memory summarizer: prefer the small/cheap draft provider
	// (tier system) when one is configured; otherwise the active
	// main provider. Resolved per call so /model swaps apply.
	summaryProviderFor := func() llm.Provider {
		if draftProvider != nil && !strings.Contains(strings.ToLower(draftProvider.Name()), "echo") {
			return draftProvider
		}
		return loop.Provider()
	}

	// Emergency dump when the console window is closed via the X:
	// CTRL_CLOSE_EVENT gives ~5s — too risky for an LLM call, so
	// the uncovered transcript tail is stored raw (no-op off
	// Windows) and summarized at the next startup (below).
	installCloseHandler(func() {
		defer recoverAndLog(dataDir)()
		dumpRawMemoryTail(memAutoSaver, loop, memProg)
	})

	// Summarize raw-log entries left behind by a previous abrupt
	// shutdown into normal task-log entries (+ USER: facts), in
	// the background — startup stays network-free.
	go func() {
		defer recoverAndLog(dataDir)()
		p := summaryProviderFor()
		if !usableSummaryProvider(p) {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		memAutoSaver.SummarizePendingRaw(ctx, providerSummarizer(p))
	}()

	// redrawStatus forces a TUI re-render so the pull-based footer
	// (Codex `limit:` tile etc.) reflects data that arrived from a
	// background goroutine. It is created here but only does anything
	// once the bubbletea program exists (program is assigned below);
	// the nil guard makes the pre-program startup refresh a no-op,
	// which is fine because the on-disk snapshot already renders on
	// the first frame.
	var program *tea.Program
	redrawStatus := func() {
		if program != nil {
			program.Send(tui.StatusRefreshMsgValue())
		}
	}

	model := tui.New(tui.Options{
		Home:     home,
		DataDir:  dataDir,
		Version:  version,
		Tier:     string(modelTier),
		Agent:    loop,
		LLM:      provider,
		Commands: mergedCommands,
		StatusFn: statusFn,
		// Incremental memory: after every finished agent turn,
		// summarize just the new fragment in the background so
		// the exit path usually has nothing left to do.
		OnRunEnd: func() {
			defer recoverAndLog(dataDir)()
			incrementalMemorySave(memAutoSaver, loop, memProg, summaryProviderFor())
		},
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
				globalPath, _ := config.FindTomlPaths(dataDir, ".")
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
			np, err := buildProvider(swapCfg, dataDir, caps)
			if err == nil {
				// Just switched models — if the new provider is Codex,
				// refresh its usage snapshot in the background so the HUD
				// reflects the newly selected model's limits promptly.
				// redrawStatus forces the footer to re-render once the
				// fetch lands, so the `limit:` tile appears on its own
				// without the user pressing a key.
				kickCodexUsageRefresh(np, redrawStatus)
			}
			return np, err
		},
		SessionStore:       sessStore,
		StatsRecorder:      draftStats,
		ProviderMgr:        provMgr,
		CapabilityRegistry: caps,
		GoalService:        goalSvc,
		ToolRegistry:       registry,
	})

	// Startup-latency tripwire: everything above must be local
	// IO only. If this ever creeps past a few hundred ms, check
	// the log for what got added to the hot path.
	log.Printf("startup: TUI ready in %s", time.Since(startupT).Round(time.Millisecond))

	program = tea.NewProgram(model, tea.WithAltScreen())

	pumpDone := make(chan struct{})
	go func() {
		defer recoverAndLog(dataDir)()
		defer close(pumpDone)
		for req := range askCh {
			program.Send(tui.AskRequestMsgFrom(req))
		}
	}()

	if _, err := program.Run(); err != nil {
		logCrash(dataDir, fmt.Errorf("tui error: %w", err))
		fmt.Fprintf(os.Stderr, "tui error: %v\n", err)
		os.Exit(1)
	}
	// B4 code guarantee: if the model never called remember this
	// session, generate a one-call summary and store it as a
	// task-log entry (plus refresh the project card). Runs BEFORE
	// the shutdown timer so the call is not cut off at 200ms.
	finalizeMemorySession(memAutoSaver, loop, memProg, summaryProviderFor())
	startPostTUIShutdownTimer(dataDir, 200*time.Millisecond)
	close(askCh)
	<-pumpDone
}

// runBatch executes a single prompt without TUI, printing the
// assistant response to stdout. Used for CI/CD and scripting.
func runBatch(prompt, home, dataDir, providerFlag, keyFlag, baseFlag, modelFlag string, echoFlag, debugFlag bool, draftMode, draftModel string) {
	_ = home // project root; data lives in dataDir
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

	caps, err := llm.NewCapabilityRegistryFromSources(dataDir, nil)
	if err != nil {
		fatal("load capabilities", err)
	}

	p, err := buildProvider(cfg, dataDir, caps)
	if err != nil {
		fatal("build provider", err)
	}

	// Build the agent loop.
	reg := tools.NewRegistry()
	// Register the thin file tools so batch mode can actually
	// exercise them (CI / live tool tests). tool_search makes the
	// rest reachable; these are the create/edit/move/copy/trash
	// family plus reads, all rooted at home and always-on.
	for _, sp := range []tools.Tool{
		tools.NewReadLines(home).Spec(),
		tools.NewReadContext(home).Spec(),
		tools.NewListDir(home).Spec(),
		tools.NewEditLine(home).Spec(),
		tools.NewInsertAfter(home).Spec(),
		tools.NewDeleteLines(home).Spec(),
		tools.NewWriteFile(home).Spec(),
		tools.NewMakeDir(home).Spec(),
		tools.NewMove(home).Spec(),
		tools.NewCopy(home).Spec(),
		tools.NewTrash(home).Spec(),
	} {
		reg.MustRegister(sp)
		reg.MarkAlwaysOn(sp.Name)
	}
	l, err := agent.NewLoop(agent.LoopConfig{
		Provider: p,
		Registry: reg,
		Caps:     caps,
		MaxSteps: 25,
		BaseDir:  home,
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
		case agent.DoneEvent:
			// Real provider-reported token usage for this run
			// (exact, not an estimate). Printed to stderr so it
			// never pollutes the assistant output on stdout.
			fmt.Fprintf(os.Stderr, "[tokens] in=%d out=%d total=%d\n",
				e.Usage.Input, e.Usage.Output, e.Usage.Total)
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
	reg.MustRegister(tools.NewListDir(root).Spec())
	reg.MustRegister(tools.NewReadLines(root).Spec())
	reg.MustRegister(tools.NewReadContext(root).Spec())
	reg.MustRegister(tools.NewEditLine(root).Spec())
	reg.MustRegister(tools.NewEditLines(root).Spec())
	reg.MustRegister(tools.NewInsertAfter(root).Spec())
	reg.MustRegister(tools.NewDeleteLines(root).Spec())
	reg.MustRegister(tools.NewWriteFile(root).Spec())
	reg.MustRegister(tools.NewMakeDir(root).Spec())
	reg.MustRegister(tools.NewMove(root).Spec())
	reg.MustRegister(tools.NewCopy(root).Spec())
	reg.MustRegister(tools.NewTrash(root).Spec())
	reg.MustRegister(tools.NewCtxExecuteTool(ctxexec.New(root), root).Spec())
	for _, name := range reg.Names() {
		reg.MarkAlwaysOn(name)
	}
	return reg
}

func buildProvider(cfg config.Config, dataDir string, caps *llm.CapabilityRegistry) (llm.Provider, error) {
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
	if cfg.Provider == config.ProviderCodex {
		// ChatGPT-subscription auth: requests route to the
		// ChatGPT backend Responses API with the OAuth bearer
		// token from <data dir>/auth.json instead of an API key.
		//
		// Multi-account: when more than one account is logged in
		// (auth.json + auth-<label>.json), build one CodexProvider
		// per account and wrap them in a round-robin RouterProvider
		// so calls spread across accounts with failover. A single
		// account returns a plain CodexProvider — zero overhead and
		// byte-for-byte the old behaviour.
		return buildCodexPool(cfg, dataDir, caps)
	}
	if cfg.Provider == config.ProviderAnthropic {
		return llm.NewAnthropic(llm.AnthropicConfig{
			BaseURL:        cfg.BaseURL,
			APIKey:         cfg.APIKey,
			Model:          cfg.Model,
			MaxTokens:      cfg.MaxTokens,
			Timeout:        cfg.Timeout,
			ConnectTimeout: cfg.ConnectTimeout,
			Capabilities:   caps,
		})
	}
	// Kilo: use IP shuffler client for proxy rotation.
	var httpClient *http.Client
	if strings.Contains(cfg.BaseURL, "api.kilo.ai") {
		httpClient = shuffler.Global.HTTPClient()
	}
	return llm.NewOpenAI(llm.OpenAIConfig{
		BaseURL:        cfg.BaseURL,
		APIKey:         llm.KiloDefaultKey(cfg.BaseURL, cfg.APIKey),
		Model:          cfg.Model,
		Timeout:        cfg.Timeout,
		ConnectTimeout: cfg.ConnectTimeout,
		HTTPClient:     httpClient,
		Capabilities:   caps,
	})
}

func providerNameForModel(caps *llm.CapabilityRegistry, model string) string {
	if caps == nil || model == "" {
		return ""
	}
	if info, ok := caps.Get(model); ok {
		return info.Provider
	}
	// RouterProvider.Name() decorates pooled accounts as
	// "model (N accounts)". Strip that display suffix for a second lookup.
	if strings.HasSuffix(strings.ToLower(model), " accounts)") {
		if i := strings.LastIndex(model, " ("); i > 0 {
			if info, ok := caps.Get(model[:i]); ok {
				return info.Provider
			}
		}
	}
	return ""
}

func isSubscriptionRuntimeProvider(p llm.Provider) bool {
	if p == nil {
		return false
	}
	_, ok := p.(interface {
		RateLimits() (llm.CodexRateLimits, bool)
	})
	return ok
}

func applyPricingMetadata(caps *llm.CapabilityRegistry, entries []pricing.PriceEntry) {
	if caps == nil || len(entries) == 0 {
		return
	}
	infos := make([]llm.ModelInfo, 0, len(entries))
	for _, e := range entries {
		infos = append(infos, llm.ModelInfo{
			ID:            e.ModelID,
			InputCost:     e.InputPer1M,
			OutputCost:    e.OutputPer1M,
			ContextLength: e.ContextLength,
			Source:        llm.SourceExternal,
			LastVerified:  e.FetchedAt,
		})
	}
	applyModelInfoMetadata(caps, infos)
}

func applyModelInfoMetadata(caps *llm.CapabilityRegistry, infos []llm.ModelInfo) {
	if caps == nil || len(infos) == 0 {
		return
	}
	for _, m := range infos {
		if m.ID == "" {
			continue
		}
		applyOneModelInfoMetadata(caps, m)
		// OpenRouter IDs are often provider/model (e.g.
		// deepseek/deepseek-chat), while the direct provider scan returns the
		// short model id (deepseek-chat) with Provider=deepseek. Mirror metadata
		// onto that existing short row when it is clearly the same provider.
		if slash := strings.IndexByte(m.ID, '/'); slash > 0 && slash < len(m.ID)-1 {
			provider, shortID := m.ID[:slash], m.ID[slash+1:]
			if existing, ok := caps.Get(shortID); ok && strings.EqualFold(existing.Provider, provider) {
				copy := m
				copy.ID = shortID
				copy.Provider = existing.Provider
				applyOneModelInfoMetadata(caps, copy)
			}
		}
	}
}

func applyOneModelInfoMetadata(caps *llm.CapabilityRegistry, m llm.ModelInfo) {
	if existing, ok := caps.Get(m.ID); ok {
		if m.InputCost > 0 {
			existing.InputCost = m.InputCost
		}
		if m.OutputCost > 0 {
			existing.OutputCost = m.OutputCost
		}
		if existing.ContextLength == 0 && m.ContextLength > 0 {
			existing.ContextLength = m.ContextLength
		}
		if existing.Provider == "" {
			existing.Provider = m.Provider
		}
		if m.LastVerified.After(existing.LastVerified) {
			existing.LastVerified = m.LastVerified
		}
		caps.Register(existing)
		return
	}
	caps.Register(m)
}

// buildCodexPool builds a Codex provider for every logged-in
// account and, when there is more than one, wraps them in a
// round-robin RouterProvider. Order is stable (ListAccounts sorts),
// default account first is not guaranteed — round-robin treats them
// equally, which is the point of spreading load across accounts.
func buildCodexPool(cfg config.Config, dataDir string, caps *llm.CapabilityRegistry) (llm.Provider, error) {
	labels, err := codexauth.ListAccounts(dataDir)
	if err != nil {
		labels = nil // fall through to the default-account path
	}

	// Count accounts that actually have usable tokens. Only a
	// genuine multi-account setup (>=2) takes the router path; a
	// single account uses the original global-manager path
	// unchanged (preserving its usage snapshot / HUD behaviour).
	var loggedIn []string
	for _, label := range labels {
		mgr := codexauth.NewManagerFor(dataDir, label, codexauth.Options{})
		if mgr.LoggedIn() {
			loggedIn = append(loggedIn, label)
		}
	}

	if len(loggedIn) > 1 {
		var pool []llm.Provider
		for _, label := range loggedIn {
			mgr := codexauth.NewManagerFor(dataDir, label, codexauth.Options{})
			// Resolve the account id from disk (no network) so each
			// provider scopes its rate-limit snapshot to its own
			// account — otherwise both accounts share one file and
			// show the same usage.
			acctID := ""
			if info, e := mgr.Account(); e == nil {
				acctID = info.AccountID
			}
			p, err := llm.NewCodex(llm.CodexConfig{
				BackendURL:     mgr.Options().BackendURL,
				Model:          cfg.Model,
				Tokens:         mgr,
				Timeout:        cfg.Timeout,
				ConnectTimeout: cfg.ConnectTimeout,
				Capabilities:   caps,
				DataDir:        dataDir,
				AccountID:      acctID,
			})
			if err != nil {
				return nil, fmt.Errorf("buildCodexPool %q: %w", label, err)
			}
			pool = append(pool, p)
		}
		log.Printf("codex: magazine across %d accounts: %v", len(pool), loggedIn)
		rt, err := llm.NewRouter(pool...)
		if err != nil {
			return nil, err
		}
		// Attach account labels so the HUD can show WHICH account is
		// active (e.g. "acct: drugie"), not just a slot number.
		rt.SetLabels(loggedIn)
		return rt, nil
	}

	// Single (or zero) account: preserve the exact original path,
	// including the global codexAuthMgr the /login command already
	// populated (carries the usage snapshot for the HUD).
	mgr := codexAuthMgr
	if mgr == nil {
		mgr = codexauth.NewManager(dataDir, codexauth.Options{})
	}
	return llm.NewCodex(llm.CodexConfig{
		BackendURL:     mgr.Options().BackendURL,
		Model:          cfg.Model,
		Tokens:         mgr,
		Timeout:        cfg.Timeout,
		ConnectTimeout: cfg.ConnectTimeout,
		Capabilities:   caps,
		DataDir:        dataDir,
	})
}

func boolPtr(b bool) *bool { return &b }

// codexUsageFetcher is satisfied by *llm.CodexProvider. It lets the
// startup / model-swap hooks refresh the Codex rate-limit snapshot
// without importing the concrete type or caring whether the active
// provider is actually Codex.
type codexUsageFetcher interface {
	FetchUsage(ctx context.Context) (llm.CodexRateLimits, error)
}

// codexUsageAllFetcher is implemented by the multi-account router: it
// refreshes the usage snapshot for EVERY account in the pool (each with
// its own token), not just the active one. When a provider implements
// it, refreshing usage fills in every account's snapshot so the pool
// aggregate counts all accounts — the whole point of the magazine
// being one combined limit. Single-account / non-router providers only
// implement codexUsageFetcher.
type codexUsageAllFetcher interface {
	FetchUsageAll(ctx context.Context) (llm.CodexRateLimits, error)
}

// refreshCodexUsage refreshes usage for all pooled accounts when prov
// is a multi-account router, otherwise just the active/only account.
// It returns the active account's snapshot and any (per-account)
// error, mirroring FetchUsage's signature so callers are unchanged.
func refreshCodexUsage(ctx context.Context, prov llm.Provider) (llm.CodexRateLimits, error) {
	if fa, ok := prov.(codexUsageAllFetcher); ok {
		return fa.FetchUsageAll(ctx)
	}
	if f, ok := prov.(codexUsageFetcher); ok {
		return f.FetchUsage(ctx)
	}
	return llm.CodexRateLimits{}, fmt.Errorf("provider has no usage")
}

// codexPoolUsageDetail returns a per-account usage breakdown when
// prov is a multi-account router, or "" otherwise. It renders an
// aligned table with a small bar for each account's 5h and 7d
// usage, marks the active account, and adds a pool total row — so
// the user sees both "this account" and "all accounts combined".
func codexPoolUsageDetail(prov llm.Provider) string {
	rt, ok := prov.(*llm.RouterProvider)
	if !ok {
		return ""
	}
	snaps, oks, active := rt.PoolUsage()
	if len(snaps) <= 1 {
		return "" // single account: the main detail already covers it
	}
	// Column width: longest account label (so the bars line up).
	nameW := len("account")
	for i := range snaps {
		if l := len(rt.LabelAt(i)); l > nameW {
			nameW = l
		}
	}
	var b strings.Builder
	b.WriteString("\n\naccounts (magazine — active drains first):\n")
	for i, s := range snaps {
		marker := "  "
		if i == active {
			marker = "▶ "
		}
		name := rt.LabelAt(i)
		if !oks[i] || !s.OK {
			fmt.Fprintf(&b, "%s%-*s   (no usage data yet)\n", marker, nameW, name)
			continue
		}
		fmt.Fprintf(&b, "%s%-*s   5h %s   7d %s\n",
			marker, nameW, name,
			usageBar(s.PrimaryUsedPct), usageBar(s.SecondaryUsedPct))
	}
	// Pool total.
	if p5, p7, n := rt.PoolAggregate(); n > 0 {
		fmt.Fprintf(&b, "  %-*s   5h %s   7d %s\n",
			nameW, "POOL", usageBar(p5), usageBar(p7))
	}
	return strings.TrimRight(b.String(), "\n")
}

// usageBar renders a used-percent as a 10-cell bar plus the number,
// e.g. 30 -> "▰▰▰▱▱▱▱▱▱▱ 30%". Clamped to 0..100.
func usageBar(pct int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := (pct + 5) / 10 // round to nearest cell
	if filled > 10 {
		filled = 10
	}
	return strings.Repeat("▰", filled) + strings.Repeat("▱", 10-filled) +
		fmt.Sprintf(" %3d%%", pct)
}

// kickCodexUsageRefresh refreshes the Codex usage snapshot in the
// background when prov is a Codex provider. It is fire-and-forget and
// deliberately silent: a failure (offline, 401, non-Codex provider)
// leaves the last on-disk snapshot in place and never blocks the
// caller or surfaces an error to the user. The HUD reads the snapshot
// pull-style, so a successful refresh shows up on the next render.
//
// This is NOT a completion — it hits the dedicated usage endpoint and
// does not consume the quota the way /responses does.
//
// notify, when non-nil, is invoked after a SUCCESSFUL fetch so the
// caller can force a TUI redraw — the HUD `limit:` tile is pulled
// from the snapshot at render time, so without a redraw a swap onto a
// Codex model would not show fresh limits until the next keystroke.
func kickCodexUsageRefresh(prov llm.Provider, notify func()) {
	// Accept either the single-account fetcher or the multi-account
	// router; refreshCodexUsage picks FetchUsageAll when available so
	// every pooled account gets fresh usage, not just the active one.
	_, single := prov.(codexUsageFetcher)
	_, all := prov.(codexUsageAllFetcher)
	if !single && !all {
		return
	}
	go func() {
		defer func() { _ = recover() }()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		// A per-account error (e.g. one expired token) is logged but
		// not fatal: accounts that succeeded still refreshed, so we
		// still redraw to show whatever fresh data we got.
		if _, err := refreshCodexUsage(ctx, prov); err != nil {
			log.Printf("codex usage refresh: %v", err)
		}
		if notify != nil {
			notify()
		}
	}()
}

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
// configured with a different Model id.
//
// F11 is OPT-IN. It engages only when BOTH:
//   - a draft mode other than "off" is set, AND
//   - a draft model is EXPLICITLY configured via
//     --draft-model (or config.toml draft_model).
//
// There is deliberately no auto-pick: an unset draft
// model means no speculative decoding. This avoids
// silently engaging a draft model the user never chose.
//
// Returns (nil, nil) when F11 is off or no draft model
// was configured — silent fallback per D1.
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
	// F11 is OPT-IN: it engages only when the user has
	// EXPLICITLY configured a draft model (via --draft-model
	// or config.toml draft_model). We deliberately do NOT
	// auto-pick a draft model from the F16 registry — an
	// unset draft model means "no speculative decoding",
	// even if a mode was supplied.
	if modelFlag == "" {
		log.Printf("F11: no draft model configured (--draft-model unset); F11 disabled (opt-in)")
		return nil, nil
	}
	draftModel := modelFlag
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

// councilPickerOptions builds the option list for the
// /council roster picker. Preferred source: models the
// provider scanner discovered per configured provider
// (labels "providerName/modelID" — buildable directly).
// Fallback when nothing has been scanned yet: every
// visible model id from the capability registry (bare
// ids; the active transport serves them). Hidden models
// are skipped; the list is capped to keep the picker
// usable.
func councilPickerOptions(provMgr *providers.Manager, caps *llm.CapabilityRegistry) []tools.AskOption {
	const maxOpts = 40
	var opts []tools.AskOption
	seen := make(map[string]struct{})
	for _, pi := range provMgr.ListConfigured(caps) {
		for _, mi := range pi.Models {
			if provMgr.IsHidden(mi.ID) {
				continue
			}
			label := pi.Name + "/" + mi.ID
			if _, ok := seen[label]; ok {
				continue
			}
			seen[label] = struct{}{}
			opts = append(opts, tools.AskOption{Label: label, Description: pi.BaseURL})
		}
	}
	if len(opts) == 0 && caps != nil {
		for _, mi := range caps.All() {
			if provMgr.IsHidden(mi.ID) {
				continue
			}
			opts = append(opts, tools.AskOption{Label: mi.ID, Description: mi.Provider})
		}
	}
	if len(opts) > maxOpts {
		opts = opts[:maxOpts]
	}
	return opts
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

// fatalUnwritableDataDir prints a clear, actionable error when the
// data directory next to the executable cannot be created or
// written (e.g. the exe sits in Program Files), then exits.
func fatalUnwritableDataDir(dir string, portable bool, err error) {
	fmt.Fprintf(os.Stderr, "ERROR: cannot write to data directory:\n  %s\n  (%v)\n\n", dir, err)
	if portable {
		fmt.Fprintln(os.Stderr, "SuperCli is portable: it stores all of its data in a supercli-data")
		fmt.Fprintln(os.Stderr, "folder next to supercli.exe. The folder the executable is in appears")
		fmt.Fprintln(os.Stderr, "to be read-only (e.g. Program Files or a network share).")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Fix one of:")
		fmt.Fprintln(os.Stderr, "  - move supercli.exe to a writable folder (e.g. C:\\Tools\\supercli\\)")
		fmt.Fprintln(os.Stderr, "  - or set SUPERCLI_HOME / --home to a writable directory")
	} else {
		fmt.Fprintln(os.Stderr, "The directory configured via --home / SUPERCLI_HOME is not writable.")
		fmt.Fprintln(os.Stderr, "Point it at a writable location.")
	}
	os.Exit(1)
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
func startPostTUIShutdownTimer(dataDir string, d time.Duration) {
	if d <= 0 {
		return
	}
	go func() {
		defer recoverAndLog(dataDir)()
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
func discoverSkillsForDoctor(home, dataDir string) []freshness.SkillEntry {
	d := tools.NewDiscoverer(home, dataDir)
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
  --draft-mode MODE               F11 draft mode: off|always|balanced|critical (default off; opt-in)
  --draft-model ID                F11 draft model id (required to enable F11; no auto-pick)
  --resume                        resume the most recent session on startup (also: /resume in the TUI)
  --version                       print version and exit
  -h, --help                      show this help

Env vars:
  SUPERCLI_LLM_PROVIDER, SUPERCLI_LLM_API_KEY, SUPERCLI_LLM_BASE_URL,
  SUPERCLI_LLM_MODEL, SUPERCLI_LLM_TEMPERATURE, SUPERCLI_LLM_STREAM,
  SUPERCLI_LLM_TIMEOUT, SUPERCLI_DEBUG, SUPERCLI_HOME

Data is stored in a single portable supercli-data/ directory next to
the executable (override with --home or SUPERCLI_HOME).
Nothing is written to %%APPDATA%% or the user home without consent.
`, version)
}
