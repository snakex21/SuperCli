package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"supercli/internal/account/usagecost"
	"supercli/internal/agent"
	"supercli/internal/assembly"
	"supercli/internal/llm"
	"supercli/internal/llm/factory"
	"supercli/internal/llm/prompt"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
	"supercli/internal/system/execution"
	"supercli/internal/system/preflight"
	"supercli/internal/system/stats"
	"supercli/internal/tools"
)

// batchAssembledHook is a test-only seam: golden tests inspect the fully
// wired batch loop (and may drive it) before the real run. Returning true
// stops runBatch there. Always nil in production.
var batchAssembledHook func(l *agent.Loop, cfg agent.LoopConfig, base *tools.Registry) bool

// runBatch executes a single prompt without TUI, printing the
// assistant response to stdout. Used for CI/CD and scripting.
func runBatch(userPrompt, home, dataDir, providerFlag, keyFlag, baseFlag, modelFlag string, echoFlag, debugFlag bool, draftMode, draftModel string, traceJSONL bool, tomlCfg config.TomlConfig) {
	_ = home // project root; data lives in dataDir
	// Echo shortcut.
	if echoFlag {
		keyFlag = ""
		providerFlag = config.ProviderEcho
	}

	// Turn-economy noop-gate (config `noop_gate`, default OFF): if the
	// working tree is byte-for-byte-fingerprint identical to the state
	// saved after the last successful run of this exact prompt, there
	// is nothing for the model to do — skip the run WITHOUT a single
	// LLM call. Fail-open: any doubt (no manifest, IO error) runs
	// normally. Interactive sessions are never gated.
	gateOn := resolveNoopGate(tomlCfg.NoopGate)
	if gateOn {
		if rec, skip := noopGateSkip(dataDir, home, userPrompt); skip {
			fmt.Printf("no-op: no changes since last identical run (%s)\n",
				rec.UpdatedAt.Local().Format("2006-01-02 15:04"))
			return
		}
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

	// Process-global LLM settings from config.toml (cache_prompt,
	// [sampling], reasoning_effort, thinking), same as the TUI path:
	// batch runs are the scripted/CI surface and must honour them too.
	// Installed before any provider is built.
	config.ApplyLLMGlobals(tomlCfg, cfg.Temperature)

	caps, err := llm.NewCapabilityRegistryFromSources(dataDir, nil)
	if err != nil {
		fatal("load capabilities", err)
	}
	// Batch shares the TUI's window-resolution sources. Without this
	// the registry never sees pricing_cache context metadata, so a
	// gateway-exclusive model (OpenCode Zen "-free" tier) resolves to
	// the generic remote fallback (128k → compact at 115k) even when
	// models.dev knows its true 200k limit. The batch variant applies
	// a stale cache synchronously — this process exits before any
	// background refresh could land.
	applyPricingStartupBatch(dataDir, caps)

	// Phase telemetry for batch runs: same recorder as the TUI,
	// dumped as one [phase] stderr line per step (plus one [calls]
	// per-purpose model-call line) after the run. The factory bakes
	// the sink into the provider it builds.
	batchStats := stats.NewMemory()
	provFactory := factory.New(buildProvider, dataDir, caps, statsCallSink(batchStats))
	p, err := provFactory.BuildChain(cfg, tomlCfg, llm.PurposeMain)
	if err != nil {
		fatal("build provider", err)
	}
	execProfile := execution.Resolve(cfg, tomlCfg, caps, envTruthy("SUPERCLI_CATALOG_HOIST"))
	// Orchestrator mode (hard delegation): resolved here so the loop is
	// built with the right contract (task as a schema-carrying core tool)
	// and the system prompt carries the orchestrator preamble. Same
	// resolution and env overrides as the TUI path.
	orchMode := resolveOrchestratorMode(tomlCfg.Orchestrator)
	if envTruthy("SUPERCLI_ORCHESTRATOR") {
		orchMode = orchestratorAlways
	}
	if envFalsey("SUPERCLI_ORCHESTRATOR") {
		orchMode = orchestratorNever
	}
	orchHard := orchMode.hard()
	learned := llm.LoadLearnedLimits(dataDir)
	llm.InitRequestBudget(dataDir)
	prefillProfiles := llm.LoadPrefillProfiles(dataDir)
	modelContexts := config.LoadModelContextStore(dataDir)
	contextProvider := config.RuntimeProviderName(tomlCfg, cfg)
	var compactProvider llm.Provider
	if compactCfg, ok := resolveCompactConfig(tomlCfg, cfg); ok {
		compactProvider, _ = provFactory.Build(compactCfg, llm.PurposeCompact)
	}

	// orchestrator_model: the coordinator (main loop) may run on a
	// different model than the chat default — the counterpart of
	// task_model, which routes the workers. Built before the loop so
	// the main provider slot carries the orchestrator.
	orchCfg, orchOverride := resolveOrchestratorModelConfig(tomlCfg, cfg)
	var orchestratorProvider llm.Provider
	if orchOverride {
		op, opErr := provFactory.Build(orchCfg, llm.PurposeMain)
		if opErr != nil {
			log.Printf("orchestrator_model: coordinator provider %q build failed: %v — using the main provider", tomlCfg.OrchestratorModel, opErr)
		} else {
			orchestratorProvider = op
			log.Printf("orchestrator_model: coordinator uses %q @ %s", op.Name(), orchCfg.BaseURL)
		}
	}
	mainProvider := p
	contextCfg := cfg
	if orchestratorProvider != nil {
		mainProvider = orchestratorProvider
		contextCfg.Provider, contextCfg.BaseURL, contextCfg.APIKey = orchCfg.Provider, orchCfg.BaseURL, orchCfg.APIKey
		contextProvider = config.RuntimeProviderName(tomlCfg, orchCfg)
	}
	configForProvider := contextWindowConfigForProvider(dataDir, tomlCfg, contextCfg, contextProvider)

	taskWorkerCfg, taskWorkerOverride := resolveTaskWorkerConfig(tomlCfg, cfg)
	var taskWorkerProvider llm.Provider
	if taskWorkerOverride {
		wp, wpErr := provFactory.Build(taskWorkerCfg, llm.PurposeTask)
		if wpErr != nil {
			log.Printf("task_model: worker provider %q build failed: %v — workers use the main provider", tomlCfg.TaskModel, wpErr)
		} else {
			taskWorkerProvider = wp
			log.Printf("task_model: delegated workers use %q @ %s", wp.Name(), taskWorkerCfg.BaseURL)
		}
	}
	var advisorProvider llm.Provider
	if advisorCfg, advisorOverride := resolveAdvisorConfig(tomlCfg, cfg); advisorOverride {
		ap, apErr := provFactory.Build(advisorCfg, llm.PurposeAdvisor)
		if apErr != nil {
			log.Printf("advisor_model: provider %q build failed: %v — advisor uses the worker provider", tomlCfg.AdvisorModel, apErr)
		} else {
			advisorProvider = ap
		}
	}

	// Build the agent loop: the batch toolset (see assembly.Batch) is the
	// thin file family always-on plus discoverable lookups; tool_search makes
	// the rest reachable.
	reg := tools.NewRegistry()
	codeIntel := tools.NewCodeIntel(home)
	defer codeIntel.Close()
	processSession := tools.NewProcessSession(home, dataDir)
	defer processSession.Close()
	defer tools.CloseBrowsers()
	assembly.Register(reg, assembly.Batch, &assembly.Deps{
		Home: home, DataDir: dataDir, Toml: tomlCfg, Registry: reg,
		CodeIntel: codeIntel, ProcessSession: processSession,
	})
	projectInstructions := prompt.LoadProjectInstructions(home)
	promptSpec := assembly.PromptSpec{
		Base:         prompt.Build(execProfile.PromptSmall),
		Platform:     assembly.PlatformHint,
		ModelProfile: prompt.LoadProfileAt(home, dataDir, cfg.Model),
		Project:      projectInstructions,
	}
	// Batch carries only the hard orchestrator preamble: an adaptive batch
	// run has no coordinator section (historical; delegation stays available
	// through the task tool's own contract).
	if orchHard {
		promptSpec.Role = assembly.RoleOrchestrator
	}
	systemPrompt := assembly.BuildSystemPrompt(promptSpec)
	// Batch runs share the CLI/GUI tool_errors.log so one-shot
	// failures land in the same corpus as interactive ones.
	batchErrorLog := openToolErrorLog(dataDir)
	defer batchErrorLog.Close()
	batchSessionID := fmt.Sprintf("batch-%d", time.Now().UnixNano())
	usageStore, sessionWriter := openSessionStack(dataDir, batchSessionID, home, mainProvider.Name(), tomlCfg)
	if usageStore != nil {
		defer usageStore.Close()
	}
	usageRates := usagecost.NewHistoryRates(dataDir)
	defer usageRates.Close()
	resolveWindow := func(model string, c config.Config) agent.ContextWindowResolution {
		return agent.ResolveContextWindowWithRuntime(model, tomlCfg.ContextWindow, 0, caps, learned, c.BaseURL, c.APIKey)
	}
	loopCfg := assembly.BuildLoopConfig(assembly.LoopSpec{
		Provider:               mainProvider,
		Registry:               reg,
		Caps:                   caps,
		ErrorLog:               batchErrorLog,
		System:                 systemPrompt,
		ProjectInstructions:    projectInstructions,
		UserInstructions:       prompt.ActiveUserInstructions(dataDir),
		UserInstructionsForRun: userInstructionsForRun(dataDir),
		// Same as the TUI: state the real workspace in the request tail so
		// the model does not guess a path and the system prefix stays
		// cacheable across projects. No memory or goal blocks: batch opens
		// neither store.
		LiveContextBlocks: func(ctx context.Context) ([]llm.ContextBlock, error) {
			return []llm.ContextBlock{{Source: llm.SourceRuntime, Text: workingDirNote}}, ctx.Err()
		},
		Toml:         tomlCfg,
		Profile:      execProfile,
		Orchestrator: orchHard,
		Home:         home,
		Stats:        batchStats,
		Writer:       sessionWriter,
		// The same source-aware cascade as TUI/WebGUI (memory-only).
		ContextWindowFor:       func(model string) agent.ContextWindowResolution { return resolveWindow(model, contextCfg) },
		ContextProvider:        contextProvider,
		ScopedContextWindowFor: assembly.ScopedContextWindow(modelContexts, configForProvider, resolveWindow),
		RefreshContextWindow: func(ctx context.Context) error {
			if taskWorkerProvider != nil {
				return config.RefreshLocalContextWindows(ctx, contextCfg, taskWorkerCfg)
			}
			return config.RefreshLocalContextWindows(ctx, contextCfg)
		},
		Summarizer:      agent.NewAutoSummarizerWithProvider(compactProvider, reg.ActiveNames),
		LearnLimit:      learned.Learn,
		PrefillProfiles: prefillProfiles,
	})
	l, err := agent.NewLoop(loopCfg)
	if err != nil {
		fatal("agent loop", err)
	}

	// Preflight repo context (config `preflight_repo`): batch runs are
	// always a fresh context, so the repo block saves the same early
	// discovery turns as in the TUI. Appended to the user prompt side.
	if resolvePreflightRepo(tomlCfg.PreflightRepo) {
		l.SetNextCoordinatorAddonSource(func(ctx context.Context) string {
			block := preflight.BuildContext(ctx, home, preflight.Options{Layout: true})
			if block != "" && ctx.Err() == nil {
				fmt.Fprintf(os.Stderr, "[preflight] repo context ~%d tok (project turn)\n", preflight.EstimateTokens(block))
			}
			return block
		})
	}

	// Orchestrator wiring — the same contract as the TUI (wireAgentTool):
	// when delegation is enabled the batch main loop gets the task tool
	// and can hand work to isolated workers whose tool calls run outside
	// the coordinator's steps. With `orchestrator = true` (hard mode, see
	// orchHard above) the main loop is trimmed to delegation + read-only
	// lookups so the coordinator context stays clean of file-edit
	// payloads. `task_model` puts workers on a small/cheap backend;
	// `draft_verify` sieves a file-changing draft with verify_commands
	// and lets this (big) model judge the diff + evidence ("small drafts,
	// big verdict").
	at, atErr := wireAgentTool(agentToolWiring{
		loop:                    l,
		registry:                reg,
		provider:                mainProvider,
		caps:                    caps,
		tomlCfg:                 tomlCfg,
		taskWorkerProvider:      taskWorkerProvider,
		taskWorkerCfg:           taskWorkerCfg,
		advisorProvider:         advisorProvider,
		home:                    home,
		coordinatorProviderName: config.RuntimeProviderName(tomlCfg, contextCfg),
		delegationOff:           !orchMode.delegationEnabled(),
		coordinatorMode:         orchMode.hard(),
	})
	if atErr != nil {
		fatal("init agent tool", atErr)
	}
	// Fresh worker briefings get the same compact repo-state block as the
	// coordinator's first turn (cold contexts benefit most).
	if resolvePreflightRepo(tomlCfg.PreflightRepo) {
		at.PreflightContext = func(ctx context.Context) string {
			return preflight.BuildContext(ctx, home, preflight.Options{Layout: true})
		}
	}
	if orchMode.hard() {
		// Coordinator contract: the main loop keeps only delegation +
		// read-only lookups; workers inherit the full registry.
		l.SetRegistry(assembly.OrchestratorRegistry(reg, false))
	}
	if batchAssembledHook != nil && batchAssembledHook(l, loopCfg, reg) {
		return
	}

	runCtx := context.Background()
	if usageStore != nil {
		runCtx = llm.WithCallSink(runCtx, func(stat llm.CallStat) {
			if stat.TokensIn == 0 && stat.TokensOut == 0 {
				return
			}
			u := usagecost.CallUsage(tomlCfg, stat, session.UsageRecord{SessionID: batchSessionID, Provider: contextProvider, ProviderType: contextCfg.Provider, Model: mainProvider.Name()})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := usageStore.AppendUsage(ctx, u); err == nil && config.EffectiveCostCurrency(tomlCfg) != "USD" && u.PriceSnapshot.AmountUSD != nil && *u.PriceSnapshot.AmountUSD > 0 {
				usageRates.WarmCurrency(config.EffectiveCostCurrency(tomlCfg), u.PriceSnapshot.UsageDay)
			}
		})
	}
	// review_model: post-hoc review of the finished change (nil = off).
	// The tree snapshot is taken here, before the main model runs.
	reviewer := newBatchReviewer(tomlCfg, cfg, provFactory, home)
	runStarted := time.Now()
	// --trace-jsonl: machine-readable tool/usage lines on stdout for
	// the eval harness. nil (flag off) makes every call a no-op.
	trace := newBatchTrace(os.Stdout, traceJSONL)
	contextProbe := newBatchContextProbe(traceJSONL)
	if contextProbe != nil {
		runCtx = llm.WithRequestObserver(runCtx, contextProbe.observe)
	}
	toolCalls := 0

	// Consume events. MessageEvent carries streaming fragments, not logical
	// lines; Print preserves the model's formatting while Println inserted a
	// newline after every token/delta on slow local streams.
	printedMessage := false
	runOnce := func(ctx context.Context, loop *agent.Loop, prompt string) batchRunResult {
		var res batchRunResult
		ch, err := loop.Run(ctx, prompt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: agent run: %v\n", err)
			res.Err = err
			return res
		}
		for ev := range ch {
			switch e := ev.(type) {
			case agent.MessageEvent:
				fmt.Print(e.Text)
				trace.noteText(e.Text)
				printedMessage = true
			case agent.ReasoningEvent:
				fmt.Printf("<thinking>%s</thinking>\n", e.Text)
				trace.noteText("\n")
				printedMessage = true
			case agent.ToolCallEvent:
				toolCalls++
				trace.tool(e.Name)
			case agent.NoticeEvent:
				// Provider status (rate-limit retry etc.) — stderr so it
				// never pollutes the assistant output on stdout.
				fmt.Fprintf(os.Stderr, "[notice] %s\n", e.Text)
			case agent.ToolResultsPrunedEvent:
				fmt.Fprintf(os.Stderr, "[prune] results=%d reclaimed=%d est=%d threshold=%d threshold_source=%s window=%d\n",
					e.Pruned, e.Reclaimed, e.Estimated, e.Threshold, e.ThresholdSource, e.Window)
			case agent.AutoCompactEvent:
				fmt.Fprintf(os.Stderr, "[compact] removed=%d reason=%s est=%d raw=%d estimate_source=%s exact_base=%d threshold=%d threshold_source=%s window=%d window_source=%s\n",
					e.Removed, e.Reason, e.Estimated, e.RawEstimated, e.EstimateSource,
					e.ExactBase, e.Threshold, e.ThresholdSource, e.Window, e.WindowSource)
			case agent.DoneEvent:
				// Real provider-reported token usage for this run
				// (exact, not an estimate). Printed to stderr so it
				// never pollutes the assistant output on stdout. The
				// cache/eval split (cache-miss telemetry) is appended
				// only when the backend reported a cached share, so the
				// baseline format stays stable for scripts.
				line := fmt.Sprintf("[tokens] in=%d out=%d total=%d",
					e.Usage.Input, e.Usage.Output, e.Usage.Total)
				if e.Usage.Cached > 0 {
					line += fmt.Sprintf(" cache=%d eval=%d", e.Usage.Cached, e.Usage.Input-e.Usage.Cached)
				}
				fmt.Fprintln(os.Stderr, line)
				res.Usage, res.Steps, res.Model = e.Usage, e.Steps, e.Model
			case agent.ErrorEvent:
				fmt.Fprintf(os.Stderr, "error: %v\n", e.Err)
				res.Usage, res.Steps, res.Err = e.Usage, e.Steps, e.Err
			}
		}
		return res
	}
	workerCtx, stopWorker := reviewer.workerContext(runCtx)
	result := runOnce(workerCtx, l, userPrompt)
	if reviewer != nil {
		result = reviewer.finish(runCtx, workerCtx, userPrompt, l, loopCfg, result, runOnce)
	}
	stopWorker()
	doneModel := result.Model
	if doneModel == "" {
		doneModel = mainProvider.Name()
	}
	usageLine := buildBatchTraceUsage(doneModel, result.Usage, result.Steps, toolCalls, batchStats.Calls(), time.Since(runStarted), result.Err != nil)
	if reviewer != nil {
		usageLine.Review = &reviewer.trace
	}
	usageLine.Context = contextProbe.report(batchStats.Calls())
	if result.Err != nil {
		// Partial accounting survives the failure for the eval
		// harness; plain batch output is unchanged.
		trace.usage(usageLine)
		os.Exit(1)
	}
	if printedMessage {
		fmt.Println()
		trace.noteText("\n")
	}
	trace.usage(usageLine)

	// Phase telemetry: one machine-greppable line per step on stderr
	// (stdout stays pure assistant output). Where each step's wall
	// time went + how many tool calls the model batched per step.
	for _, t := range batchStats.Snapshot() {
		if line := stats.PhaseLine(t); line != "" {
			fmt.Fprintln(os.Stderr, line)
		}
	}
	// Purpose-labeled model-call ledger: every Complete call this
	// run (main + helper inferences), aggregated per purpose.
	if line := stats.CallsLine(batchStats.Calls()); line != "" {
		fmt.Fprintln(os.Stderr, line)
	}
	for _, call := range batchStats.Calls() {
		if line := stats.PrefillLine(call); line != "" {
			fmt.Fprintln(os.Stderr, line)
		}
	}

	// Successful run: record the post-run tree fingerprint so the next
	// identical prompt over an unchanged tree can be skipped for free.
	if gateOn {
		noopGateSave(dataDir, home, userPrompt)
	}
}
