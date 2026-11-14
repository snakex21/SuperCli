package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercli/internal/account/credits"
	"supercli/internal/account/tier"
	"supercli/internal/agent"
	"supercli/internal/agent/darwin"
	"supercli/internal/assembly"
	"supercli/internal/assembly/goldentest"
	"supercli/internal/checkpoint"
	"supercli/internal/llm"
	"supercli/internal/llm/factory"
	"supercli/internal/llm/prompt"
	"supercli/internal/storage"
	"supercli/internal/storage/goal"
	"supercli/internal/storage/memory"
	"supercli/internal/system/config"
	"supercli/internal/system/execution"
	systemhostpower "supercli/internal/system/hostpower"
	"supercli/internal/system/stats"
	"supercli/internal/tools"
)

// The goldens below pin the composition of the CLI surfaces (interactive TUI
// and --batch) for every execution profile. They were captured before the
// shared runtime refactor and must stay byte-identical after it.

// saveAppGlobals snapshots the process-level composition state the CLI keeps
// in package variables and restores it after the test.
func saveAppGlobals(t *testing.T) {
	t.Helper()
	base, profile, user, project := supercliSystemPromptBase, supercliModelProfile, supercliUserInstructions, supercliProjectInstructions
	coord, orch, off := supercliCoordinatorMode, supercliOrchestratorMode, supercliDelegationDisabled
	brief, note := memoryBriefing, workingDirNote
	t.Cleanup(func() {
		supercliSystemPromptBase, supercliModelProfile, supercliUserInstructions, supercliProjectInstructions = base, profile, user, project
		supercliCoordinatorMode, supercliOrchestratorMode, supercliDelegationDisabled = coord, orch, off
		memoryBriefing, workingDirNote = brief, note
		tools.SetDefaultApprover(nil)
	})
}

// goldenFlags are the flags of `supercli --echo --model M --base-url U`.
func goldenFlags(home, dataDir string) cliFlags {
	return cliFlags{Home: home, DataDir: dataDir, Echo: true, Model: goldentest.Model, BaseURL: goldentest.BaseURL, DraftMode: "off"}
}

// offlinePricing keeps the background pricing refresh from reaching the
// network during a golden run.
func offlinePricing(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:9")
	t.Setenv("NO_PROXY", "")
}

func TestRuntimeGolden_Batch(t *testing.T) {
	for _, p := range goldentest.Profiles {
		t.Run(p.Name, func(t *testing.T) {
			goldentest.CleanEnv(t)
			offlinePricing(t)
			saveAppGlobals(t)
			home, dataDir := goldentest.Fixture(t, p)
			n := goldentest.NewNormalizer(home, dataDir)
			flags := goldenFlags(home, dataDir)
			// parseCLIFlags default: coordinator mode on.
			supercliCoordinatorMode = true
			ws := resolveWorkspace(flags)
			// Main: the batch approval gate is installed before runBatch.
			gate := installApprovalGate(ws.DataDir, nil, ws.UILanguage, "batch mode (--batch)", flags.Unsandboxed)

			var out string
			batchAssembledHook = func(l *agent.Loop, cfg agent.LoopConfig, base *tools.Registry) bool {
				out += goldentest.Section("surface", "batch profile="+p.Name)
				out += goldentest.Section("base registry", goldentest.Registry(base, n))
				out += goldentest.Section("loop visible tools", strings.Join(l.VisibleToolNames(), "\n"))
				out += goldentest.Section("system prompt", n.Apply(cfg.System))
				out += goldentest.Section("project instructions", n.Apply(cfg.ProjectInstructions))
				out += goldentest.Section("user instructions", n.Apply(cfg.UserInstructions))
				out += goldentest.Section("loop config", goldentest.LoopConfig(cfg, n))
				out += goldentest.Section("approval gate", goldentest.ApprovalGate(gate, n))
				scripted := &goldentest.ScriptedProvider{ModelName: llm.ProviderModelName(l.Provider())}
				if err := l.SetAccountProvider(scripted); err != nil {
					t.Fatal(err)
				}
				out += goldentest.Section("events", goldentest.Drive(t, l, goldentest.CallMarker+`tool_search {"query":"web_fetch"}`, "thanks"))
				out += goldentest.Section("transcript", scripted.Transcript(n))
				return true
			}
			t.Cleanup(func() { batchAssembledHook = nil })
			runBatch("golden", ws.Home, ws.DataDir, flags.Provider, flags.Key, flags.BaseURL, flags.Model, flags.Echo, flags.Debug, flags.DraftMode, flags.DraftModel, flags.TraceJSONL, ws.Toml)
			if out == "" {
				t.Fatal("batch hook did not run")
			}
			goldentest.Check(t, filepath.Join(goldentest.Dir("testdata"), "batch_"+p.Name+".golden"), out)
		})
	}
}

func TestRuntimeGolden_TUI(t *testing.T) {
	for _, p := range goldentest.Profiles {
		t.Run(p.Name, func(t *testing.T) {
			goldentest.CleanEnv(t)
			offlinePricing(t)
			saveAppGlobals(t)
			home, dataDir := goldentest.Fixture(t, p)
			n := goldentest.NewNormalizer(home, dataDir)
			rt := assembleInteractiveForGolden(t, goldenFlags(home, dataDir))
			n.Literal(rt.sessionID, "sess-<ID>")
			out := goldentest.Section("surface", "tui profile="+p.Name)
			out += goldentest.Section("base registry", goldentest.Registry(rt.registry, n))
			out += goldentest.Section("checkpoint wrapped", rt.wrapped.String())
			out += goldentest.Section("loop visible tools", strings.Join(rt.loop.VisibleToolNames(), "\n"))
			out += goldentest.Section("system prompt", n.Apply(rt.loopCfg.System))
			out += goldentest.Section("project instructions", n.Apply(rt.loopCfg.ProjectInstructions))
			out += goldentest.Section("user instructions", n.Apply(rt.loopCfg.UserInstructions))
			out += goldentest.Section("darwin system prompt", n.Apply(rt.darwinSystem))
			out += goldentest.Section("loop config", goldentest.LoopConfig(rt.loopCfg, n))
			out += goldentest.Section("approval gate", goldentest.ApprovalGate(rt.gate, n))

			// Transcript on a second, identically assembled runtime.
			rt2 := assembleInteractiveForGolden(t, goldenFlags(home, dataDir))
			n.Literal(rt2.sessionID, "sess-<ID>")
			scripted := &goldentest.ScriptedProvider{ModelName: llm.ProviderModelName(rt2.loop.Provider())}
			if err := rt2.loop.SetAccountProvider(scripted); err != nil {
				t.Fatal(err)
			}
			out += goldentest.Section("events", goldentest.Drive(t, rt2.loop, goldentest.CallMarker+`tool_search {"query":"web_fetch"}`, "thanks"))
			out += goldentest.Section("transcript", scripted.Transcript(n))
			goldentest.Check(t, filepath.Join(goldentest.Dir("testdata"), "tui_"+p.Name+".golden"), out)
		})
	}
}

type goldenInteractive struct {
	loop         *agent.Loop
	registry     *tools.Registry
	loopCfg      agent.LoopConfig
	gate         *tools.ApprovalGate
	sessionID    string
	darwinSystem string
	wrapped      *goldentest.WrapRecorder
}

// assembleInteractiveForGolden mirrors Main from flag resolution to the
// orchestrator registry swap, minus the bubbletea program, slash commands,
// dashboard and background idle work (none of which register tools or touch
// LoopConfig). The checkpoint wrapper records which tools it wraps instead of
// snapshotting a git worktree.
func assembleInteractiveForGolden(t *testing.T, flags cliFlags) goldenInteractive {
	t.Helper()
	supercliCoordinatorMode = true // parseCLIFlags default
	ws := resolveWorkspace(flags)
	home, dataDir := ws.Home, ws.DataDir
	cwd, uiLanguage := ws.Cwd, ws.UILanguage
	tomlCfg, tomlErr := ws.Toml, ws.TomlErr
	activeProject, hasActiveProject := ws.ActiveProject, ws.HasActiveProject

	db, err := storage.OpenAt(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	creditStorage := credits.NewStorage(db)
	if err := creditStorage.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	goalStorage := goal.NewStorage(db)
	if err := goalStorage.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	goalSvc := goal.NewProjectService(goalStorage, memory.ProjectStorageKey(dataDir, home))
	_, _ = goalSvc.Refresh(context.Background())

	cfg := applyRuntimeConfig(&flags, tomlCfg, tomlErr, activeProject, hasActiveProject)
	caps, err := llm.NewCapabilityRegistryFromSources(dataDir, db)
	if err != nil {
		caps = llm.NewCapabilityRegistry()
	}
	draftStats := stats.NewMemory()
	callSink := statsCallSink(draftStats)
	provFactory := factory.New(buildProvider, dataDir, caps, callSink)
	provider, err := provFactory.BuildChain(cfg, tomlCfg, llm.PurposeMain)
	if err != nil {
		t.Fatal(err)
	}
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
	supercliProjectInstructions = prompt.LoadProjectInstructions(home)

	orchMode := resolveOrchestratorMode(tomlCfg.Orchestrator)
	supercliOrchestratorMode = orchMode.hard()
	supercliDelegationDisabled = !orchMode.delegationEnabled()
	if supercliOrchestratorMode {
		supercliCoordinatorMode = true
	} else if supercliDelegationDisabled {
		supercliCoordinatorMode = false
	}

	sessionID := fmt.Sprintf("sess-%d", time.Now().UnixNano())
	recorder := &goldentest.WrapRecorder{}
	checkpointSpec := recorder.Wrap
	var checkpointCtrl *checkpoint.Controller

	registry := tools.NewRegistry()
	toolset := newInteractiveToolset(registry, home, dataDir, tomlCfg, checkpointSpec)
	askCh, toolSearcher := toolset.AskCh, toolset.ToolSearcher
	t.Cleanup(func() {
		toolset.CodeIntel.Close()
		toolset.ProcessSession.Close()
		tools.CloseBrowsers()
		toolset.FTSClose()
	})
	gate := installApprovalGate(dataDir, askCh, uiLanguage, "", false)
	if err := toolSearcher.RebuildIndex(); err != nil {
		t.Fatal(err)
	}
	logsDir := filepath.Join(dataDir, "logs")
	errorLog := openToolErrorLog(dataDir)
	t.Cleanup(func() { errorLog.Close() })

	applyAlwaysOnToolProfile(registry, supercliCoordinatorMode, execProfile.ThinTools)
	memBundle := openMemoryStack(dataDir, home, cfg.APIKey, smallTier, tomlCfg)
	t.Cleanup(func() {
		if memBundle.Global != nil {
			memBundle.Global.Close()
		}
		if memBundle.Project != nil {
			memBundle.Project.Close()
		}
	})
	memStore := memBundle.Project
	memoryBriefing = memBundle.Briefing
	hostPower := systemhostpower.New(systemhostpower.Options{Execute: func(context.Context) error { return nil }})
	t.Cleanup(func() { hostPower.Close() })

	darwinSystem := buildSystemPromptWithInstructions(goalSvc, "")
	darwinTool := darwin.NewDarwinTool(provider, registry, home, darwinSystem)
	darwinTool.SetUserInstructionsForRun(userInstructionsForRun(dataDir))
	darwinTool.SetReasoningEffort(tomlCfg.DarwinReasoningEffort)
	darwinTool.SetPoolSize(tomlCfg.DarwinPoolSize)
	darwinTool.SetLoopFactory(darwin.AgentLoopAdapter(registry, func(root string) (*tools.Registry, error) {
		return assembly.ChildToolset(root), nil
	}))
	darwinSequential := llm.IsLocalBaseURL(cfg.BaseURL)
	if tomlCfg.DarwinParallel != nil {
		darwinSequential = !*tomlCfg.DarwinParallel
	}
	darwinTool.SetSequential(darwinSequential)
	darwinTool.SetAllowAutoMerge(tomlCfg.DarwinAutoMerge != nil && *tomlCfg.DarwinAutoMerge)
	var darwinSpec *tools.Tool
	if config.DarwinEnabled(tomlCfg) {
		spec := darwinTool.Spec()
		darwinSpec = &spec
	}

	taskWorkerCfg, taskWorkerOverride := resolveTaskWorkerConfig(tomlCfg, cfg)
	var taskWorkerProvider llm.Provider
	if taskWorkerOverride {
		if wp, wpErr := provFactory.Build(taskWorkerCfg, llm.PurposeTask); wpErr == nil {
			taskWorkerProvider = wp
		} else {
			taskWorkerCfg = cfg
		}
	}
	var compactProvider llm.Provider
	if compactCfg, ok := resolveCompactConfig(tomlCfg, cfg); ok {
		compactProvider, _ = provFactory.Build(compactCfg, llm.PurposeCompact)
	}
	mainProvider := provider
	coordinatorCfg := cfg
	taskParallel, taskParallelWarnLocal := execution.Parallel(taskWorkerCfg.BaseURL, tomlCfg.TaskParallel)

	injector := startPatternInjector(memStore, dataDir, logsDir)
	tracker := credits.NewTracker(sessionID, credits.Budget{}, creditStorage)
	t.Cleanup(func() { tracker.Close() })
	llm.InitRequestBudget(dataDir)
	sessStore, sessWriter := openSessionStack(dataDir, sessionID, home, cfg.Model, tomlCfg)
	if sessStore != nil {
		t.Cleanup(func() { sessStore.Close() })
	}
	toolset.registerCore(memBundle, goalSvc, func(g *tools.GoalTool) {
		g.SetDecompose(goalProviderAdapter{Provider: provider}, provider.Name())
	}, darwinSpec, sessStore)
	dr := wireDraftAndReflection(flags.DraftMode, flags.DraftModel, provider, provFactory, cfg, tierRules, tomlCfg, dataDir)
	cw := wireContextWindows(dataDir, coordinatorCfg, tomlCfg, caps, compactProvider, registry, taskWorkerProvider, dr.Provider)

	loopCfg := buildMainLoopConfig(loopAssembly{
		provider:             mainProvider,
		registry:             registry,
		goalSvc:              goalSvc,
		memoryBriefing:       memoryBriefing,
		memoryBriefingForRun: memBundle.BriefingForRun,
		tomlCfg:              tomlCfg,
		errorLog:             errorLog,
		reflector:            dr.Reflector,
		reflectEvery:         dr.ReflectEvery,
		adaptiveReflection:   dr.AdaptiveReflection,
		injector:             injector,
		tracker:              tracker,
		sessWriter:           sessWriter,
		draftPolicy:          dr.Policy,
		draftProvider:        dr.Provider,
		navigatorProvider:    cw.NavigatorProvider,
		draftSink:            dr.Sink,
		draftStats:           draftStats,
		contextWindowFor:     cw.ContextWindowFor,
		refreshContextWindow: func(ctx context.Context) error {
			return config.RefreshLocalContextWindows(ctx, coordinatorCfg)
		},
		initialContextProvider: cw.InitialContextProvider,
		scopedContextWindowFor: cw.ScopedContextWindowFor,
		autoSummarizer:         cw.AutoSummarizer,
		learned:                cw.Learned,
		prefillProfiles:        cw.PrefillProfiles,
		execProfile:            execProfile,
		taskParallel:           taskParallel,
		taskParallelWarnLocal:  taskParallelWarnLocal,
		home:                   home,
		dataDir:                dataDir,
	})
	loop, err := agent.NewLoop(loopCfg)
	if err != nil {
		t.Fatal(err)
	}
	loop.ConfigureHostPower(hostPower, goalSvc, false)
	configureInvocationPersistence(loop, sessStore, checkpointCtrl)
	if _, err := wireAgentTool(agentToolWiring{
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
		checkpoint:         checkpointSpec,
	}); err != nil {
		t.Fatal(err)
	}
	if mcpManager := initMcp(dataDir, tomlCfg, registry, func() { _ = toolSearcher.RebuildIndex() }); mcpManager != nil {
		t.Cleanup(mcpManager.StopAll)
	}
	provMgr := setupProviderManager(dataDir, cwd, caps)
	buildCouncilMember := makeCouncilMemberBuilder(provMgr, caps, cfg, provFactory)
	wireConsultTool(provider, caps, cfg, provFactory, buildCouncilMember, loop, registry)
	toolset.registerLate()
	darwinTool.SetProviderResolver(loop.Provider)
	loop.SetExternalSink(make(chan agent.Event, 16))
	if supercliOrchestratorMode {
		loop.SetRegistry(assembly.OrchestratorRegistry(registry, true))
	}
	return goldenInteractive{loop: loop, registry: registry, loopCfg: loopCfg, gate: gate, sessionID: sessionID, darwinSystem: darwinSystem, wrapped: recorder}
}
