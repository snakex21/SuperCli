package app

import (
	"context"
	"fmt"
	"os"
	"strings"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/llm/factory"
	"supercli/internal/llm/prompt"
	"supercli/internal/system/config"
	"supercli/internal/system/execution"
	"supercli/internal/system/preflight"
	"supercli/internal/system/stats"
	"supercli/internal/tools"
	"supercli/internal/tools/ctxexec"
)

// runBatch executes a single prompt without TUI, printing the
// assistant response to stdout. Used for CI/CD and scripting.
func runBatch(userPrompt, home, dataDir, providerFlag, keyFlag, baseFlag, modelFlag string, echoFlag, debugFlag bool, draftMode, draftModel string, tomlCfg config.TomlConfig) {
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
	learned := llm.LoadLearnedLimits(dataDir)
	modelContexts := config.LoadModelContextStore(dataDir)
	contextProvider := config.RuntimeProviderName(tomlCfg, cfg)
	var compactProvider llm.Provider
	if compactCfg, ok := resolveCompactConfig(tomlCfg, cfg); ok {
		compactProvider, _ = provFactory.Build(compactCfg, llm.PurposeCompact)
	}

	// Build the agent loop.
	reg := tools.NewRegistry()
	codeIntel := tools.NewCodeIntel(home)
	defer codeIntel.Close()
	processSession := tools.NewProcessSession(home)
	defer processSession.Close()
	// Register the thin file tools so batch mode can actually
	// exercise them (CI / live tool tests). tool_search makes the
	// rest reachable; these are the create/edit/move/copy/trash
	// family plus reads, all rooted at home and always-on.
	for _, sp := range []tools.Tool{
		tools.NewReadLines(home).Spec(),
		tools.NewReadContext(home).Spec(),
		tools.NewReadMany(home).Spec(),
		tools.NewScratchpad(home).Spec(),
		tools.NewListDir(home).Spec(),
		tools.NewPatchFile(home).Spec(),
		tools.NewCreateFile(home).Spec(),
		tools.NewWriteFile(home).Spec(),
		tools.NewMakeDir(home).Spec(),
		tools.NewMove(home).Spec(),
		tools.NewCopy(home).Spec(),
		tools.NewTrash(home).Spec(),
		tools.NewSearchCode(home).Spec(),
		tools.NewCtxExecuteTool(ctxexec.New(home), home).Spec(),
	} {
		sp = codeIntel.WrapMutation(sp)
		reg.MustRegister(sp)
		reg.MarkAlwaysOn(sp.Name)
	}
	reg.MustRegister(codeIntel.Spec())
	reg.MustRegister(processSession.Spec())
	reg.MustRegister(agent.NewInvokeTool(reg).Spec())
	reg.MarkAlwaysOn("invoke_tool")
	// A compact current-facts tool is always available in batch mode. The
	// advanced web_search/web_fetch contracts stay discoverable on demand.
	reg.MustRegister(tools.NewWebFetch().Spec())
	batchWebEngine := tomlCfg.WebSearch.Engine
	batchWebKey := tomlCfg.WebSearch.APIKey
	if batchWebKey == "" {
		switch strings.ToLower(batchWebEngine) {
		case "brave":
			batchWebKey = os.Getenv("BRAVE_API_KEY")
		case "tavily":
			batchWebKey = os.Getenv("TAVILY_API_KEY")
		}
	}
	batchWeb := tools.NewWebSearch(batchWebEngine, batchWebKey, tomlCfg.WebSearch.BaseURL)
	reg.MustRegister(batchWeb.Spec())
	reg.MustRegister(batchWeb.LookupSpec())
	reg.MarkAlwaysOn("web_lookup")
	// A short-lived batch registry does not need a SQLite FTS database: the
	// searcher uses its deterministic lexical fallback over this small set.
	toolSearcher := tools.NewToolSearcher(reg, nil)
	reg.MustRegister(toolSearcher.Spec())
	reg.MarkAlwaysOn("tool_search")
	systemPrompt := prompt.Build(execProfile.PromptSmall)
	if modelProfile := prompt.LoadProfileAt(home, dataDir, cfg.Model); modelProfile != "" {
		systemPrompt += "\n\n" + modelProfile
	}
	if instructions := prompt.ActiveUserInstructions(dataDir); instructions != "" {
		systemPrompt += "\n\n" + instructions
	}
	// Batch runs share the CLI/GUI tool_errors.log so one-shot
	// failures land in the same corpus as interactive ones.
	batchErrorLog := openToolErrorLog(dataDir)
	defer batchErrorLog.Close()
	l, err := agent.NewLoop(agent.LoopConfig{
		Provider:              p,
		Registry:              reg,
		Caps:                  caps,
		ErrorLog:              batchErrorLog,
		System:                systemPrompt,
		MaxSteps:              tomlCfg.MaxStepsOr(25),
		BaseDir:               home,
		Stats:                 batchStats,
		EnableNavigator:       execProfile.EnableNavigator,
		NavigatorAuto:         execProfile.NavigatorAuto,
		NavigatorKeywordsOnly: execProfile.NavigatorKeywordsOnly,
		ThinTools:             execProfile.ThinTools,
		StableToolset:         execProfile.StableToolset,
		CatalogHoist:          execProfile.CatalogHoist,
		// Batch uses the same source-aware cascade as TUI/WebGUI. This is
		// intentionally local and latency-free: config > cached model catalog >
		// learned provider limit > conservative loop fallback.
		ContextWindowFor: func(model string) agent.ContextWindowResolution {
			return agent.ResolveContextWindow(model, tomlCfg.ContextWindow, 0, caps, learned, cfg.BaseURL)
		},
		ContextProvider: contextProvider,
		ScopedContextWindowFor: func(provider, model string) agent.ContextWindowResolution {
			if tokens, ok := modelContexts.Get(provider, model); ok {
				return agent.ContextWindowResolution{Tokens: tokens, Source: "model-override"}
			}
			return agent.ContextWindowResolution{}
		},
		Summarizer:         agent.NewAutoSummarizerWithProvider(compactProvider, reg.ActiveNames),
		LearnLimit:         learned.Learn,
		PruneProtectTokens: tomlCfg.PruneProtectTokens,
	})
	if err != nil {
		fatal("agent loop", err)
	}

	// Preflight repo context (config `preflight_repo`): batch runs are
	// always a fresh context, so the repo block saves the same early
	// discovery turns as in the TUI. Appended to the user prompt side.
	preflightTurn := !execProfile.EnableNavigator
	if execProfile.EnableNavigator {
		mode, confident := agent.DefaultRouteMap().ClassifyConfident(userPrompt)
		preflightTurn = !confident || mode == agent.RouteCoordinator
	}
	if resolvePreflightRepo(tomlCfg.PreflightRepo) && preflightTurn {
		if block := preflight.Build(home, preflight.Options{}); block != "" {
			l.SetNextCoordinatorAddon(block)
			fmt.Fprintf(os.Stderr, "[preflight] repo context ~%d tok (project turn)\n", preflight.EstimateTokens(block))
		}
	}

	ch, err := l.Run(context.Background(), userPrompt)
	if err != nil {
		fatal("agent run", err)
	}

	// Consume events. MessageEvent carries streaming fragments, not logical
	// lines; Print preserves the model's formatting while Println inserted a
	// newline after every token/delta on slow local streams.
	printedMessage := false
	for ev := range ch {
		switch e := ev.(type) {
		case agent.MessageEvent:
			fmt.Print(e.Text)
			printedMessage = true
		case agent.NoticeEvent:
			// Provider status (rate-limit retry etc.) — stderr so it
			// never pollutes the assistant output on stdout.
			fmt.Fprintf(os.Stderr, "[notice] %s\n", e.Text)
		case agent.ToolResultsPrunedEvent:
			fmt.Fprintf(os.Stderr, "[prune] results=%d reclaimed=%d est=%d window=%d\n",
				e.Pruned, e.Reclaimed, e.Estimated, e.Window)
		case agent.AutoCompactEvent:
			fmt.Fprintf(os.Stderr, "[compact] removed=%d reason=%s est=%d raw=%d estimate_source=%s exact_base=%d threshold=%d window=%d window_source=%s\n",
				e.Removed, e.Reason, e.Estimated, e.RawEstimated, e.EstimateSource,
				e.ExactBase, e.Threshold, e.Window, e.WindowSource)
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
		case agent.ErrorEvent:
			fmt.Fprintf(os.Stderr, "error: %v\n", e.Err)
			os.Exit(1)
		}
	}
	if printedMessage {
		fmt.Println()
	}

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

	// Successful run: record the post-run tree fingerprint so the next
	// identical prompt over an unchanged tree can be skipped for free.
	if gateOn {
		noopGateSave(dataDir, home, userPrompt)
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
	reg.MustRegister(tools.NewReadMany(root).Spec())
	reg.MustRegister(tools.NewScratchpad(root).Spec())
	reg.MustRegister(tools.NewPatchFile(root).Spec())
	reg.MustRegister(tools.NewCreateFile(root).Spec())
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
