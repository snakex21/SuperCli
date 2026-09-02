// SuperCli — single-binary, portable, sandboxed CLI AI agent.
//
// Usage:
//
//	supercli [--home PATH] [--data-dir PATH] [--provider P] [--model M]
//	         [--key K] [--base-url U] [--status] [--doctor] [--echo] [--debug]
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
// The resolved home is only the working-directory sandbox. Runtime state
// (config, sessions, memory, auth) lives beside this executable in
// supercli-data, unless --data-dir/SUPERCLI_DATA_DIR explicitly overrides it.
package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/account/credits"
	"supercli/internal/account/tier"
	"supercli/internal/agent"
	"supercli/internal/agent/darwin"
	"supercli/internal/buildinfo"
	"supercli/internal/checkpoint"
	"supercli/internal/llm"
	"supercli/internal/llm/factory"
	"supercli/internal/llm/prompt"
	"supercli/internal/storage"
	"supercli/internal/storage/freshness"
	"supercli/internal/storage/goal"
	"supercli/internal/system/childproc"
	"supercli/internal/system/execution"
	"supercli/internal/system/stats"
	"supercli/internal/tools"
	"supercli/internal/tools/ctxexec"
	"supercli/internal/ui/tui"
)

var version = buildinfo.Version

func Main() {
	startupT := time.Now()
	// ABSOLUTE FIRST thing: catch ANY panic and log it.
	// If the program crashes silently, check supercli-data/logs/crash.log
	// next to the executable. `dataDir` is captured by the closure:
	// until the data root is resolved we fall back to the portable
	// default; afterwards the crash log lands in the resolved data
	// dir, same as every other crash path (crash.go).
	dataDir := storage.PortableDataRoot()
	defer func() {
		if r := recover(); r != nil {
			logCrash(dataDir, r)
			fmt.Fprintf(os.Stderr, "\nFATAL: %v\nCheck %s for stack trace.\n", r, crashLogPath(dataDir))
			os.Exit(1)
		}
	}()

	// Pipeline: flags -> workspace/config -> short-circuits -> runtime cfg -> onboarding -> TUI.
	flags := parseCLIFlags()
	if flags.ShowVersion {
		printVersion()
		return
	}

	ws := resolveWorkspace(flags)
	home, dataDir := ws.Home, ws.DataDir
	cwd, uiLanguage := ws.Cwd, ws.UILanguage
	tomlCfg, tomlErr := ws.Toml, ws.TomlErr
	activeProject, hasActiveProject := ws.ActiveProject, ws.HasActiveProject

	initCodexAuth(dataDir, tomlCfg)
	appLog := initAppLog(dataDir)
	if appLog != nil {
		defer appLog.Close()
	}

	// Orphan-process journal: every long-lived child (MCP/LSP/pty) is
	// recorded in <dataDir>/processes.jsonl. This startup sweep
	// terminates children whose owner process is gone — a crash,
	// taskkill /F or power loss never runs the normal Stop path.
	// Windows job objects cover clean shutdowns; this covers the rest
	// (and everything on Unix). Runs before any spawn in this process.
	childproc.SetJournal(filepath.Join(dataDir, childproc.JournalFile))
	if n := childproc.Sweep(); n > 0 {
		log.Printf("orphan sweep: terminated %d leftover subprocess(es) from a previous run", n)
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
	if flags.Doctor {
		// F18: build staleness report from
		// discovered skills (no provider needed).
		checker := freshness.NewChecker()
		skills := discoverSkillsForDoctor(home, dataDir)
		report := checker.RunReport(nil, skills, nil)
		runDoctor(home, dataDir, creditStorage, &report)
		return
	}

	// --status short-circuits before the TUI.
	if flags.Status {
		if err := runStatus(home, creditStorage); err != nil {
			fatal("status", err)
		}
		return
	}

	// --batch short-circuits: run prompt without TUI.
	if flags.Batch != "" {
		runBatch(flags.Batch, home, dataDir, flags.Provider, flags.Key, flags.BaseURL, flags.Model, flags.Echo, flags.Debug, flags.DraftMode, flags.DraftModel, tomlCfg)
		return
	}

	cfg := applyRuntimeConfig(&flags, tomlCfg, tomlErr, activeProject, hasActiveProject)
	maybeRunOnboarding(flags.Echo, &cfg, &tomlCfg, dataDir, cwd, uiLanguage)

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
	if flags.ModelInfo != "" {
		runModelInfo(caps, flags.ModelInfo)
		return
	}

	// F16: --list-models prints the registry.
	// With --refresh, we fetch /v1/models from
	// the configured provider and register the
	// heuristic capabilities before printing.
	if flags.ListModels {
		runListModels(caps, cfg.BaseURL, cfg.APIKey, cfg.Provider, flags.Refresh)
		return
	}

	// Per-turn telemetry recorder (F28 /cost + phase timings + the
	// purpose-labeled model-call ledger). Created BEFORE the provider
	// factory so every Complete call in the process — main step calls
	// AND helper inferences (navigator, compact, reflection, draft,
	// memory autosave, goal, judges, consult samples) — lands here.
	// Historically only the F11 draft savings flowed in, hence the
	// old name kept at the call sites below.
	draftStats := stats.NewMemory()
	callSink := statsCallSink(draftStats)
	// Central provider factory: EVERY provider in the process (main,
	// /model swaps, task workers, draft, council members, consult
	// samples) is built here and comes out wrapped in llm.Metered, so
	// purpose labels, the background gate and foreground preemption
	// apply uniformly. Capability probing (Codex usage fetchers,
	// RouterProvider pool) unwraps via llm.Unwrap.
	provFactory := factory.New(buildProvider, dataDir, caps, callSink)
	provider, err := provFactory.BuildChain(cfg, tomlCfg, llm.PurposeMain)
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
	execProfile := execution.Resolve(cfg, tomlCfg, caps, envTruthy("SUPERCLI_CATALOG_HOIST"))
	supercliSystemPromptBase = prompt.Build(execProfile.PromptSmall)
	supercliModelProfile = prompt.LoadProfileAt(home, dataDir, cfg.Model)
	supercliUserInstructions = prompt.ActiveUserInstructions(dataDir)

	// Orchestrator mode (hard delegation): resolved from config, with an
	// env override for scripted/test use. When on, the main loop gets a
	// restricted registry below (delegation + read-only lookups only).
	orchMode := resolveOrchestratorMode(tomlCfg.Orchestrator)
	if envTruthy("SUPERCLI_ORCHESTRATOR") {
		orchMode = orchestratorAlways
	}
	if envFalsey("SUPERCLI_ORCHESTRATOR") {
		orchMode = orchestratorNever
	}
	supercliOrchestratorMode = orchMode.hard()
	supercliDelegationDisabled = !orchMode.delegationEnabled()
	if supercliOrchestratorMode {
		// Hard orchestration always carries the coordinator contract.
		supercliCoordinatorMode = true
	} else if supercliDelegationDisabled {
		// Explicit "never" must not leave the adaptive coordinator prompt
		// asking for tools that are deliberately absent below.
		supercliCoordinatorMode = false
	}

	sessionID := fmt.Sprintf("sess-%d", time.Now().UnixNano())
	var checkpointCtrl *checkpoint.Controller
	if manager, cpErr := checkpoint.Open(home, dataDir); cpErr == nil {
		checkpointCtrl = checkpoint.NewController(manager, sessionID)
	} else if cpErr != checkpoint.ErrUnavailable {
		log.Printf("checkpoint: %v", cpErr)
	}
	checkpointSpec := func(spec tools.Tool) tools.Tool {
		if checkpointCtrl != nil {
			return checkpointCtrl.Wrap(spec)
		}
		return spec
	}

	registry := tools.NewRegistry()
	wrap := toolWrappers{
		checkpoint: checkpointSpec,
		// diagnostic is set after codeIntel is created below.
	}
	askCh, codeIntel, processSession, toolSearcher, ftsClose := registerMediaAndOfficeTools(
		registry, home, dataDir, caps, wrap,
	)
	defer codeIntel.Close()
	defer processSession.Close()
	if ftsClose != nil {
		defer ftsClose()
	}
	// wrap.diagnostic is set inside registerMediaAndOfficeTools once
	// codeIntel exists; late file tools below use wrap.mut.
	_ = askCh

	if err := toolSearcher.RebuildIndex(); err != nil {
		log.Printf("rebuild fts index: %v", err)
	}

	logsDir := filepath.Join(dataDir, "logs")
	errorLog := openToolErrorLog(dataDir)
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

	// Execution-profile-aware visibility + dual memory stack.
	applyAlwaysOnToolProfile(registry, supercliCoordinatorMode, execProfile.ThinTools)
	memBundle := openMemoryStack(dataDir, home, cfg.APIKey, smallTier, tomlCfg)
	if memBundle.Global != nil {
		defer memBundle.Global.Close()
	}
	if memBundle.Project != nil {
		defer memBundle.Project.Close()
	}
	globalMemStore := memBundle.Global
	memStore := memBundle.Project
	memAutoSaver := memBundle.AutoSaver
	memProg := memBundle.Progress
	memoryBriefing = memBundle.Briefing
	registerMemoryTools(registry, memBundle)

	// F10: ctx_execute is the context-mode sandbox.
	// The model uses it instead of file_read for
	// large files: writes a small script, gets
	// bounded stdout back. Always-on so it sees the
	// token-savings tool from turn 1.
	ctxRunner := ctxexec.New(home)
	ctxTool := tools.NewCtxExecuteTool(ctxRunner, home)
	registry.MustRegister(checkpointSpec(ctxTool.Spec()))

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
	// Local backends run best-of-N sequentially: a single GPU serializes
	// the requests anyway (N× wall time), and interleaved contexts thrash
	// each other's KV cache. Auto-detected from the base URL; a config
	// override (darwin_parallel) wins in both directions for self-hosted
	// servers behind a public address or a forced choice.
	darwinSequential := llm.IsLocalBaseURL(cfg.BaseURL)
	if tomlCfg.DarwinParallel != nil {
		darwinSequential = !*tomlCfg.DarwinParallel
	}
	darwinTool.SetSequential(darwinSequential)
	if darwinSequential {
		log.Printf("darwin: local backend — sequential best-of-N (expect ~N× time)")
	}
	registry.MustRegister(darwinTool.Spec())

	// Task delegation parallelism mirrors darwin: a batch of `task` calls
	// in one turn runs concurrently on cloud backends but sequentially on
	// local ones (one GPU slot serializes them anyway; interleaved worker
	// contexts thrash the KV cache). Auto-detected from the base URL;
	// task_parallel (tri-state) overrides in both directions. Forcing
	// parallel on a local backend is unusual — the loop warns once when it
	// actually runs such a batch.
	// Model-per-task (config `task_model`): delegated workers may run on
	// a different model/host than the coordinator. Resolved here so the
	// parallelism auto-gate below looks at the WORKER's backend — that
	// is where a task batch actually executes. Construction is a pure
	// client build (no network); reachability is probed lazily on the
	// first delegation. A build failure is a warning, never a hard
	// error: workers then inherit the coordinator's provider. The
	// worker provider carries the WORKER's base URL, so host-gated
	// behaviour (the cache_prompt auto-detection inside llm.NewOpenAI)
	// follows the worker's host, not the coordinator's.
	taskWorkerCfg, taskWorkerOverride := resolveTaskWorkerConfig(tomlCfg, cfg)
	var taskWorkerProvider llm.Provider
	if taskWorkerOverride {
		// Worker calls run outside the coordinator's steps; the
		// "task" purpose keeps them visible (and separable) in
		// the session's model-call ledger.
		wp, wpErr := provFactory.Build(taskWorkerCfg, llm.PurposeTask)
		if wpErr != nil {
			log.Printf("task_model: worker provider %q build failed: %v — workers use the main provider", tomlCfg.TaskModel, wpErr)
			taskWorkerCfg = cfg // parallel gate falls back to the real backend too
		} else {
			taskWorkerProvider = wp
			log.Printf("task_model: delegated workers use %q @ %s", wp.Name(), taskWorkerCfg.BaseURL)
		}
	}
	compactCfg, compactOverride := resolveCompactConfig(tomlCfg, cfg)
	var compactProvider llm.Provider
	if compactOverride {
		cp, cpErr := provFactory.Build(compactCfg, llm.PurposeCompact)
		if cpErr != nil {
			log.Printf("compact_model: provider %q build failed: %v — compaction uses the main provider", tomlCfg.CompactModel, cpErr)
		} else {
			compactProvider = cp
			log.Printf("compact_model: summaries use %q @ %s", cp.Name(), compactCfg.BaseURL)
		}
	}
	// orchestrator_model: the coordinator (main loop) may run on a
	// different model than the chat default — the counterpart of
	// task_model, which routes the workers. The orchestrator writes the
	// task brief and the worker executes it in an isolated context.
	// Built before the loop so the main provider slot carries the
	// orchestrator when one is configured.
	orchCfg, orchOverride := resolveOrchestratorModelConfig(tomlCfg, cfg)
	var orchestratorProvider llm.Provider
	if orchOverride {
		op, opErr := provFactory.Build(orchCfg, llm.PurposeMain)
		if opErr != nil {
			log.Printf("orchestrator_model: coordinator provider %q build failed: %v — the main model keeps coordinating", tomlCfg.OrchestratorModel, opErr)
		} else {
			orchestratorProvider = op
			log.Printf("orchestrator_model: coordinator uses %q @ %s", op.Name(), orchCfg.BaseURL)
		}
	}
	mainProvider := provider
	if orchestratorProvider != nil {
		mainProvider = orchestratorProvider
	}
	taskParallel, taskParallelWarnLocal := execution.Parallel(taskWorkerCfg.BaseURL, tomlCfg.TaskParallel)

	// cache_prompt and [sampling] are installed process-globally by
	// applyRuntimeConfig (config.ApplyLLMGlobals), which runs before
	// the main provider is built — NewOpenAI reads both at
	// construction. Providers built later this session (a /model swap,
	// a task_model worker, a compact_model summarizer, a failover hop)
	// read the same globals.

	// Warm KV cache across sessions (llama.cpp slot save/restore).
	// nil for cloud endpoints — they are never probed. slot_cache
	// (config.toml, tri-state) overrides the local-host auto-gate.
	// The first failed call (server without --slot-save-path, old
	// build, network error) disables it for the rest of the process:
	// the errors below land in the log file only, never in the TUI.
	slotCache := llm.NewSlotCache(cfg.BaseURL, tomlCfg.SlotCache)

	injector := startPatternInjector(memStore, dataDir, logsDir)

	// F7: build the credit tracker. The session id is
	// the cwd-based default; for F7 we do not yet
	// support --resume so each launch is a new
	// session.
	budget := credits.Budget{PerSession: flags.MaxSession, PerDay: flags.MaxDay}
	if err := budget.Validate(); err != nil {
		fatal("credit budget", err)
	}
	tracker := credits.NewTracker(sessionID, budget, creditStorage)
	if err := creditStorage.SaveBudget(context.Background(), sessionID, budget); err != nil {
		log.Printf("save budget: %v", err)
	}
	defer tracker.Close()

	applyPricingStartup(dataDir, caps)

	// Daily per-endpoint request counter (request_budget.json) —
	// feeds "N/100 today" style quota visibility for metered
	// providers such as the OpenCode Zen free tier.
	llm.InitRequestBudget(dataDir)

	// F13: session store + optional search_history tool.
	sessStore, sessWriter := openSessionStack(dataDir, sessionID, home, cfg.Model, registry)
	if sessStore != nil {
		defer sessStore.Close()
	}

	// F11 draft + F5.a reflection + Wave 4 context windows.
	dr := wireDraftAndReflection(flags.DraftMode, flags.DraftModel, provider, provFactory, cfg, tierRules, tomlCfg, dataDir)
	draftPolicy, draftProvider, draftSink := dr.Policy, dr.Provider, dr.Sink
	reflectEvery, adaptiveReflection, reflector := dr.ReflectEvery, dr.AdaptiveReflection, dr.Reflector
	cw := wireContextWindows(dataDir, cfg, tomlCfg, caps, compactProvider, registry, taskWorkerProvider, draftProvider)
	learned, modelContexts := cw.Learned, cw.ModelContexts
	initialContextProvider := cw.InitialContextProvider
	contextWindowFor, scopedContextWindowFor := cw.ContextWindowFor, cw.ScopedContextWindowFor
	windowFor, autoSummarizer := cw.WindowFor, cw.AutoSummarizer
	navigatorProvider := cw.NavigatorProvider
	if navigatorProvider != nil {
		log.Printf("navigator: route classification uses side provider %q", navigatorProvider.Name())
	}

	// Build the real loop. Pass the home as the image base dir.
	loop, err := agent.NewLoop(buildMainLoopConfig(loopAssembly{
		provider:               mainProvider,
		registry:               registry,
		goalSvc:                goalSvc,
		memoryBriefing:         memoryBriefing,
		tomlCfg:                tomlCfg,
		errorLog:               errorLog,
		reflector:              reflector,
		reflectEvery:           reflectEvery,
		adaptiveReflection:     adaptiveReflection,
		injector:               injector,
		tracker:                tracker,
		sessWriter:             sessWriter,
		draftPolicy:            draftPolicy,
		draftProvider:          draftProvider,
		navigatorProvider:      navigatorProvider,
		draftSink:              draftSink,
		draftStats:             draftStats,
		contextWindowFor:       contextWindowFor,
		initialContextProvider: initialContextProvider,
		scopedContextWindowFor: scopedContextWindowFor,
		autoSummarizer:         autoSummarizer,
		learned:                learned,
		prefillProfiles:        cw.PrefillProfiles,
		execProfile:            execProfile,
		taskParallel:           taskParallel,
		taskParallelWarnLocal:  taskParallelWarnLocal,
		home:                   home,
	}))
	if err != nil {
		fatal("init agent", err)
	}

	at, err := wireAgentTool(agentToolWiring{
		loop:               loop,
		registry:           registry,
		provider:           mainProvider,
		caps:               caps,
		tomlCfg:            tomlCfg,
		taskWorkerProvider: taskWorkerProvider,
		taskWorkerCfg:      taskWorkerCfg,
		home:               home,
		delegationOff:      supercliDelegationDisabled,
		coordinatorMode:    supercliCoordinatorMode,
	})
	if err != nil {
		fatal("init agent tool", err)
	}

	// F14: /clear slash command. Hides all but the last
	// 2 user turns from the model's view. Cheaper than
	// "new session" because the TUI scrollback, the
	// FTS5 search index, and the on-disk session.db
	// all stay intact.
	mergedCommands := mergedSlashCommands(darwinTool, goalSvc, home)
	wireSlashEarly(mergedCommands, slashWireDeps{
		home:           home,
		dataDir:        dataDir,
		cwd:            cwd,
		sessionID:      sessionID,
		uiLanguage:     uiLanguage,
		tomlCfg:        tomlCfg,
		loop:           loop,
		at:             at,
		tracker:        tracker,
		provider:       provider,
		caps:           caps,
		sessStore:      sessStore,
		windowFor:      windowFor,
		slotCache:      slotCache,
		injector:       injector,
		registry:       registry,
		memStore:       memStore,
		globalMemStore: globalMemStore,
		memoryBriefing: memoryBriefing,
		askCh:          askCh,
	})

	// MCP client: merge configured servers with portable dataDir/mcp packages,
	// expose one thin bridge, and start each stdio process only on first use.
	// /mcp remains the human-readable status/restart surface.
	reindexTools := func() {
		if err := toolSearcher.RebuildIndex(); err != nil {
			log.Printf("mcp: tool index rebuild: %v", err)
		}
	}
	mcpManager := initMcp(dataDir, tomlCfg, registry, reindexTools)
	if mcpManager != nil {
		defer mcpManager.StopAll()
	}
	mergedCommands["mcp"] = mcpCommand(mcpManager)

	// --resume: load the most recent prior session at startup.
	if flags.Resume && sessStore != nil {
		if recent, err := sessStore.ListRecent(context.Background(), 2); err == nil {
			for _, r := range recent {
				if r.ID == sessionID {
					continue
				}
				if msg, err := resumeSession(context.Background(), loop, sessStore, windowFor, r.ID); err == nil {
					log.Printf("--resume: %s", msg)
					if n, rerr := slotCache.Restore(context.Background(), r.ID); rerr != nil {
						log.Printf("slotcache: restore %s: %v (disabled for this session)", r.ID, rerr)
					} else if n > 0 {
						log.Printf("slotcache: restored %d cached token(s) for %s", n, r.ID)
					}
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

	// Model menus are handled directly by the TUI: /model is the fast
	// picker of enabled models, while /models is the complete visibility
	// catalog. The old text-only handler would only duplicate that state.

	// F30 provider manager + F12 council/consult tool.
	provMgr := setupProviderManager(dataDir, cwd, caps)
	buildCouncilMember := makeCouncilMemberBuilder(provMgr, caps, cfg, provFactory)
	council := wireConsultTool(provider, caps, cfg, provFactory, buildCouncilMember, loop, registry)

	wireSlashLate(mergedCommands, slashWireDeps{
		home:               home,
		dataDir:            dataDir,
		cwd:                cwd,
		sessionID:          sessionID,
		uiLanguage:         uiLanguage,
		tomlCfg:            tomlCfg,
		loop:               loop,
		at:                 at,
		tracker:            tracker,
		provider:           provider,
		caps:               caps,
		sessStore:          sessStore,
		windowFor:          windowFor,
		slotCache:          slotCache,
		injector:           injector,
		registry:           registry,
		memStore:           memStore,
		globalMemStore:     globalMemStore,
		memoryBriefing:     memoryBriefing,
		askCh:              askCh,
		provMgr:            provMgr,
		council:            council,
		buildCouncilMember: buildCouncilMember,
	})

	registerFileWebAndLineTools(registry, home, tomlCfg, wrap, toolSearcher)

	// F7 + F8 status bar (goal, credits, tokens, ctx, codex limits, workers).
	projName := ""
	if hasActiveProject {
		projName = activeProject.Name
	}
	statusFn := buildStatusFn(statusBarDeps{
		goalSvc:          goalSvc,
		loop:             loop,
		tracker:          tracker,
		draftStats:       draftStats,
		caps:             caps,
		home:             home,
		hasActiveProject: hasActiveProject,
		projectName:      projName,
		windowFor:        windowFor,
		agentTool:        at,
	})

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

	// Background memory work — startup raw-log summarization AND
	// the incremental per-turn summary — runs ONLY when the user
	// has been idle for memoryIdleDelay. On a single local model
	// an autosave inference fired right after the answer competes
	// with the user's next question (worse TTFT, KV-prefix churn);
	// deferring it to the idle window keeps the foreground path
	// clean. A new prompt cancels the in-flight call; the
	// uncovered fragment is retried (turns batched into one
	// summary) at the next idle window, so nothing is ever lost.
	var rawSummarized atomic.Bool
	memIdle := newIdleScheduler(memoryIdleDelay, func(ctx context.Context) {
		defer recoverAndLog(dataDir)()
		p := summaryProviderFor()
		if !usableSummaryProvider(p) {
			return
		}
		// Raw-log entries left behind by a previous abrupt
		// shutdown (or a normal exit — see finalizeMemorySession)
		// are summarized first, once per process. A canceled pass
		// keeps its raw entries and retries at the next idle.
		if !rawSummarized.Load() {
			rctx, cancel := context.WithTimeout(ctx, 60*time.Second)
			memAutoSaver.SummarizePendingRaw(rctx, providerSummarizer(p))
			cancel()
			if ctx.Err() == nil {
				rawSummarized.Store(true)
			}
		}
		incrementalMemorySave(ctx, memAutoSaver, loop, memProg, p)
	})
	// Startup backlog waits for idle too — never in the way of the
	// user's first question.
	memIdle.Schedule()

	// Orchestrator mode: hand the MAIN loop a restricted registry now
	// that every tool — including the late-registered task/file tools —
	// is present in the full base registry. The AgentTool keeps the full
	// `registry` as its worker BaseRegistry, so workers stay fully
	// capable; only the coordinator is trimmed to delegation + read-only
	// lookups. Done as a one-shot swap before the first Run so the tool
	// list is fixed for the whole session (KV-cache prefix stability).
	if supercliOrchestratorMode {
		loop.SetRegistry(agent.OrchestratorRegistry(registry))
	}

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

	model := tui.New(buildTUIOptions(tuiLaunchDeps{
		home:                   home,
		dataDir:                dataDir,
		sessionID:              sessionID,
		version:                version,
		uiLanguage:             uiLanguage,
		modelTier:              modelTier,
		loop:                   loop,
		provider:               provider,
		mergedCommands:         mergedCommands,
		statusFn:               statusFn,
		checkpointCtrl:         checkpointCtrl,
		memAutoSaver:           memAutoSaver,
		memProg:                memProg,
		memIdle:                memIdle,
		extCh:                  extCh,
		sessStore:              sessStore,
		draftStats:             draftStats,
		provMgr:                provMgr,
		initialContextProvider: initialContextProvider,
		modelContexts:          modelContexts,
		caps:                   caps,
		goalSvc:                goalSvc,
		registry:               registry,
		provFactory:            provFactory,
		cfg:                    cfg,
		redrawStatus:           redrawStatus,
	}))

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
	afterTUIShutdown(slotCache, loop, sessionID, memIdle, memAutoSaver, memProg, dataDir, askCh, pumpDone)
}
