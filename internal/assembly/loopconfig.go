package assembly

import (
	"context"

	"supercli/internal/agent"
	"supercli/internal/agent/ultrawork"
	"supercli/internal/llm"
	"supercli/internal/llm/draft"
	"supercli/internal/system/config"
	"supercli/internal/system/execution"
	"supercli/internal/system/stats"
	"supercli/internal/tools"
)

// LoopSpec is the surface-neutral input of BuildLoopConfig. Fields shared by
// every surface are derived here once (step cap, execution-profile flags,
// navigator gating, prune budget, workspace roots); per-surface differences
// are the explicit fields below.
type LoopSpec struct {
	Provider llm.Provider
	Registry *tools.Registry
	// Caps is the capability registry. The interactive TUI leaves it nil
	// (historical: it resolves capabilities through its context-window
	// resolvers only); batch and web pass theirs.
	Caps *llm.CapabilityRegistry

	System                 string
	ProjectInstructions    string
	UserInstructions       string
	UserInstructionsForRun func(context.Context) (string, error)
	LiveContextBlocks      func(context.Context) ([]llm.ContextBlock, error)

	Toml    config.TomlConfig
	Profile execution.Profile
	// NavigatorProvider is the optional side provider for route
	// classification (TUI only). With one, keyword-only routing is off.
	NavigatorProvider llm.Provider
	Orchestrator      bool
	// TaskParallel* come from execution.Parallel on the worker backend. Batch
	// leaves them unset (one prompt); the web loop re-resolves them for a
	// task_model worker after construction.
	TaskParallel, TaskParallelWarnLocal bool

	Home            string
	InitialMessages []llm.Message
	Writer          agent.SessionWriter
	ErrorLog        agent.ErrorLogger
	Stats           stats.Recorder
	Review          *agent.PostReviewConfig

	ContextWindowFor       func(model string) agent.ContextWindowResolution
	ContextProvider        string
	ScopedContextWindowFor func(provider, model string) agent.ContextWindowResolution
	RefreshContextWindow   func(context.Context) error
	Summarizer             agent.Summarizer
	LearnLimit             func(model string, limit int)
	PrefillProfiles        *llm.PrefillProfiles

	// Interactive carries the TUI-only machinery (reflection, drafts,
	// credits, ultrawork, pattern injection); nil elsewhere.
	Interactive *InteractiveLoop
}

// InteractiveLoop holds the LoopConfig fields only the interactive TUI sets.
type InteractiveLoop struct {
	Reflector          agent.Reflector
	ReflectEvery       int
	AdaptiveReflection bool
	PatternInjector    agent.PatternInjector
	CreditTracker      agent.CreditTracker
	Ultrawork          *ultrawork.Wiring
	Draft              *draft.Policy
	DraftProvider      llm.Provider
	DraftOverrideSink  agent.DraftOverrideSink
}

// BuildLoopConfig assembles agent.LoopConfig from spec.
func BuildLoopConfig(s LoopSpec) agent.LoopConfig {
	cfg := agent.LoopConfig{
		Provider:               s.Provider,
		Registry:               s.Registry,
		Caps:                   s.Caps,
		System:                 s.System,
		ProjectInstructions:    s.ProjectInstructions,
		UserInstructions:       s.UserInstructions,
		UserInstructionsForRun: s.UserInstructionsForRun,
		LiveContextBlocks:      s.LiveContextBlocks,
		RefreshContextWindow:   s.RefreshContextWindow,
		// One shared runaway safety net (agent.DefaultMaxSteps) on every
		// surface; an explicit max_steps in config.toml stays a strict cap.
		MaxSteps:        s.Toml.MaxStepsOr(agent.DefaultMaxSteps),
		InitialMessages: s.InitialMessages,
		EnableNavigator: s.Profile.EnableNavigator,
		NavigatorAuto:   s.Profile.NavigatorAuto,
		// Keyword-only routing is the economical default unless a dedicated
		// side provider can classify without thrashing the main KV cache.
		NavigatorKeywordsOnly: s.Profile.NavigatorKeywordsOnly && s.NavigatorProvider == nil,
		NavigatorProvider:     s.NavigatorProvider,
		// Thin tools, stable toolset, catalog hoist and tail placement come
		// from the one execution profile, so every surface sends the same
		// prompt/tool contract for the same model and host.
		ThinTools:              s.Profile.ThinTools,
		StableToolset:          s.Profile.StableToolset,
		CatalogHoist:           s.Profile.CatalogHoist,
		ReplaceContextTail:     s.Profile.ReplaceContextTail,
		Orchestrator:           s.Orchestrator,
		TaskParallel:           s.TaskParallel,
		TaskParallelWarnLocal:  s.TaskParallelWarnLocal,
		BaseDir:                s.Home,
		UserDownloadsDir:       agent.SystemDownloadsDir,
		Writer:                 s.Writer,
		ErrorLog:               s.ErrorLog,
		ContextWindowFor:       s.ContextWindowFor,
		ContextProvider:        s.ContextProvider,
		ScopedContextWindowFor: s.ScopedContextWindowFor,
		Summarizer:             s.Summarizer,
		LearnLimit:             s.LearnLimit,
		PrefillProfiles:        s.PrefillProfiles,
		// Zero-LLM tool-result prune: 0 scales with the window, negative = off.
		PruneProtectTokens: s.Toml.PruneProtectTokens,
		Stats:              s.Stats,
		Review:             s.Review,
	}
	if x := s.Interactive; x != nil {
		cfg.Reflector = x.Reflector
		cfg.ReflectEvery = x.ReflectEvery
		cfg.AdaptiveReflection = x.AdaptiveReflection
		cfg.PatternInjector = x.PatternInjector
		cfg.CreditTracker = x.CreditTracker
		cfg.Ultrawork = x.Ultrawork
		cfg.Draft = x.Draft
		cfg.DraftProvider = x.DraftProvider
		cfg.DraftOverrideSink = x.DraftOverrideSink
	}
	return cfg
}

// ScopedContextWindow resolves the context window of a (provider, model)
// pair: an explicit per-model override clamped to the runtime capacity, else
// the cascade resolver for that provider's endpoint, else the default (a
// worker never borrows the coordinator's endpoint limit).
func ScopedContextWindow(models *config.ModelContextStore, configFor func(provider string) config.Config, resolve func(model string, cfg config.Config) agent.ContextWindowResolution) func(provider, model string) agent.ContextWindowResolution {
	return func(provider, model string) agent.ContextWindowResolution {
		scopedCfg := configFor(provider)
		if tokens, ok := models.Get(provider, model); ok {
			return agent.ClampContextWindowToRuntime(agent.ContextWindowResolution{Tokens: tokens, Source: "model-override"}, scopedCfg.BaseURL, scopedCfg.APIKey, model)
		}
		resolved := resolve(model, scopedCfg)
		if resolved.Tokens <= 0 {
			return agent.ContextWindowResolution{Tokens: agent.DefaultContextWindow(), Source: "fallback"}
		}
		return resolved
	}
}
