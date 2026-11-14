package app

import (
	"context"
	"strings"

	"supercli/internal/account/credits"
	"supercli/internal/agent"
	"supercli/internal/agent/ultrawork"
	"supercli/internal/assembly"
	"supercli/internal/llm"
	"supercli/internal/llm/draft"
	"supercli/internal/llm/prompt"
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
	memoryBriefingForRun   func() string
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
	refreshContextWindow   func(context.Context) error
	initialContextProvider string
	scopedContextWindowFor func(provider, model string) agent.ContextWindowResolution
	autoSummarizer         agent.Summarizer
	learned                *learnedLimits
	prefillProfiles        *llm.PrefillProfiles
	execProfile            execution.Profile
	taskParallel           bool
	taskParallelWarnLocal  bool
	home                   string
	dataDir                string
	// review is the interactive post-work review (review_model); nil = off.
	review *agent.PostReviewConfig
}

// buildMainLoopConfig assembles LoopConfig for the interactive CLI agent.
// Construction of reflector/draft/window resolvers stays in Main; this only
// packages the already-resolved pieces so Main stays free of a 70-line literal.
func buildMainLoopConfig(a loopAssembly) agent.LoopConfig {
	// Freeze all other prompt layers once. Reload only user-owned presets at
	// the next run boundary, keeping unchanged prefixes byte-stable and cacheable.
	return assembly.BuildLoopConfig(assembly.LoopSpec{
		Provider:               a.provider,
		Registry:               a.registry,
		System:                 buildSystemPromptLayers(nil, "", false),
		ProjectInstructions:    supercliProjectInstructions,
		UserInstructions:       prompt.ActiveUserInstructions(a.dataDir),
		UserInstructionsForRun: userInstructionsForRun(a.dataDir),
		RefreshContextWindow:   a.refreshContextWindow,
		LiveContextBlocks: func(ctx context.Context) ([]llm.ContextBlock, error) {
			// Read per run, so /allow-all is reflected on the next turn.
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			briefing := a.memoryBriefing
			if a.memoryBriefingForRun != nil {
				briefing = a.memoryBriefingForRun()
			}
			blocks := []llm.ContextBlock{{Source: llm.SourceMemory, Text: briefing}}
			if a.goalSvc != nil {
				if _, err := a.goalSvc.Refresh(ctx); err != nil {
					return nil, err
				}
				current, err := a.goalSvc.Inject(ctx, "", 5)
				if err != nil {
					return nil, err
				}
				blocks = append(blocks, llm.ContextBlock{Source: llm.SourceGoalState, Text: strings.TrimSpace(current)})
			}
			return append(blocks, llm.ContextBlock{Source: llm.SourceRuntime, Text: workingDirNote}), ctx.Err()
		},
		Toml:              a.tomlCfg,
		Profile:           a.execProfile,
		NavigatorProvider: a.navigatorProvider,
		// Orchestrator: task carries a full schema from turn 1. The registry
		// restriction itself is applied by Main via SetRegistry once the
		// task tools are registered into the full base registry.
		Orchestrator: supercliOrchestratorMode,
		// Task delegation parallelism: concurrent on cloud backends,
		// sequential on local ones; task_parallel overrides both ways.
		TaskParallel:           a.taskParallel,
		TaskParallelWarnLocal:  a.taskParallelWarnLocal,
		Home:                   a.home,
		Writer:                 a.sessWriter,
		ErrorLog:               a.errorLog,
		Stats:                  a.draftStats,
		Review:                 a.review,
		ContextWindowFor:       a.contextWindowFor,
		ContextProvider:        a.initialContextProvider,
		ScopedContextWindowFor: a.scopedContextWindowFor,
		Summarizer:             a.autoSummarizer,
		LearnLimit:             a.learned.Learn,
		PrefillProfiles:        a.prefillProfiles,
		Interactive: &assembly.InteractiveLoop{
			Reflector:          a.reflector,
			ReflectEvery:       a.reflectEvery,
			AdaptiveReflection: a.adaptiveReflection,
			PatternInjector:    a.injector,
			CreditTracker:      a.tracker,
			Ultrawork: &ultrawork.Wiring{
				Goal:        ultraworkGoalAdapter{svc: a.goalSvc},
				Credit:      ultraworkCreditAdapter{tracker: a.tracker},
				SisyphusMax: 3,
			},
			Draft:             a.draftPolicy,
			DraftProvider:     a.draftProvider,
			DraftOverrideSink: a.draftSink,
		},
	})
}
