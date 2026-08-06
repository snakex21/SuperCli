package app

import (
	"supercli/internal/account/credits"
	"supercli/internal/agent"
	"supercli/internal/agent/ultrawork"
	"supercli/internal/llm"
	"supercli/internal/llm/draft"
	"supercli/internal/storage/goal"
	"supercli/internal/system/config"
	"supercli/internal/system/execution"
	"supercli/internal/system/stats"
	"supercli/internal/tools"
)

// loopAssembly is the input for assembling agent.LoopConfig at CLI startup.
type loopAssembly struct {
	provider               llm.Provider
	registry               *tools.Registry
	goalSvc                *goal.Service
	memoryBriefing         string
	tomlCfg                config.TomlConfig
	errorLog               agent.ErrorLogger
	reflector              agent.Reflector
	reflectEvery           int
	adaptiveReflection     bool
	injector               agent.PatternInjector
	tracker                *credits.Tracker
	sessWriter             agent.SessionWriter
	draftPolicy            *draft.Policy
	draftProvider          llm.Provider
	navigatorProvider      llm.Provider
	draftSink              agent.DraftOverrideSink
	draftStats             *stats.Memory
	contextWindowFor       func(model string) agent.ContextWindowResolution
	initialContextProvider string
	scopedContextWindowFor func(provider, model string) agent.ContextWindowResolution
	autoSummarizer         agent.Summarizer
	learned                *learnedLimits
	execProfile            execution.Profile
	taskParallel           bool
	taskParallelWarnLocal  bool
	home                   string
}

// buildMainLoopConfig assembles LoopConfig for the interactive CLI agent.
// Construction of reflector/draft/window resolvers stays in Main; this only
// packages the already-resolved pieces so Main stays free of a 70-line literal.
func buildMainLoopConfig(a loopAssembly) agent.LoopConfig {
	// One shared runaway safety net (agent.DefaultMaxSteps) on every surface.
	// An explicit max_steps in config.toml stays a strict user cap.
	maxSteps := a.tomlCfg.MaxStepsOr(agent.DefaultMaxSteps)
	return agent.LoopConfig{
		Provider:           a.provider,
		Registry:           a.registry,
		System:             buildSystemPrompt(a.goalSvc),
		Briefing:           a.memoryBriefing,
		MaxSteps:           maxSteps,
		ErrorLog:           a.errorLog,
		Reflector:          a.reflector,
		ReflectEvery:       a.reflectEvery,
		AdaptiveReflection: a.adaptiveReflection,
		PatternInjector:    a.injector,
		CreditTracker:      a.tracker,
		Writer:             a.sessWriter,
		Ultrawork: &ultrawork.Wiring{
			Goal:        ultraworkGoalAdapter{svc: a.goalSvc},
			Credit:      ultraworkCreditAdapter{tracker: a.tracker},
			SisyphusMax: 3,
		},
		Draft:                  a.draftPolicy,
		DraftProvider:          a.draftProvider,
		NavigatorProvider:      a.navigatorProvider,
		DraftOverrideSink:      a.draftSink,
		Stats:                  a.draftStats,
		ContextWindowFor:       a.contextWindowFor,
		ContextProvider:        a.initialContextProvider,
		ScopedContextWindowFor: a.scopedContextWindowFor,
		Summarizer:             a.autoSummarizer,
		LearnLimit:             a.learned.Learn,
		// Zero-LLM tool-result prune (first line of context defense,
		// before the summary fallback). Config prune_protect_tokens:
		// 0 = scale with the active model window, negative = off.
		PruneProtectTokens:    a.tomlCfg.PruneProtectTokens,
		EnableNavigator:       a.execProfile.EnableNavigator,
		NavigatorAuto:         a.execProfile.NavigatorAuto,
		NavigatorKeywordsOnly: a.execProfile.NavigatorKeywordsOnly && a.navigatorProvider == nil,
		// Thin tool protocol: small-tier models get the compact
		// catalog + full schemas only for the core; they suffer most
		// from schema bulk in the prefill. Big models keep native
		// JSON tool calling with full schemas. Mirrors the same
		// smallTier gate that already trims their always-on set.
		ThinTools: a.execProfile.ThinTools,
		// Stable toolset: keep the request `tools` list fixed all
		// session so tool_search activations don't invalidate the
		// server-side KV prompt cache. `stable_toolset = true|false`
		// in config.toml overrides the built-in default.
		StableToolset: a.execProfile.StableToolset,
		// Catalog hoist (default for thin+stable profiles): with the stable
		// toolset, move the thin-tools preamble (sentinel instruction
		// + dormant catalog) into the KV-cached system prefix instead
		// of re-injecting it behind the growing history every step
		// (where llama.cpp re-evaluates it on every call). Enabled after
		// HP Z6/Qwen3.5-122B measured 86.4% fewer evaluated warm-turn
		// tokens with both quality arms passing. stable_toolset=false
		// or small_full_tools=true disables the automatic profile.
		CatalogHoist: a.execProfile.CatalogHoist,
		// Orchestrator: task carries a full schema from turn 1 (delegation
		// is the main loop's primary action). The registry restriction
		// itself is applied below via SetRegistry, once the task tools are
		// registered into the full base registry.
		Orchestrator: supercliOrchestratorMode,
		// Task delegation parallelism: multiple `task` calls in one turn
		// run concurrently on cloud backends, sequentially on local ones
		// (a single GPU serializes them on one slot anyway, N× wall time,
		// and interleaved worker contexts thrash the KV cache). The
		// config override (task_parallel) wins in both directions; forcing
		// parallel on a local backend warns once at execution time.
		TaskParallel:          a.taskParallel,
		TaskParallelWarnLocal: a.taskParallelWarnLocal,
		BaseDir:               a.home,
	}
}
