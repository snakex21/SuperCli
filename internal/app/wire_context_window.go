package app

import (
	"context"
	"sync"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/system/config"
	"supercli/internal/tools"
)

// contextWindowBundle is Wave 4 context-window resolution + auto-compact.
type contextWindowBundle struct {
	Learned                *learnedLimits
	ModelContexts          *config.ModelContextStore
	PrefillProfiles        *llm.PrefillProfiles
	InitialContextProvider string
	ContextWindowFor       func(model string) agent.ContextWindowResolution
	ScopedContextWindowFor func(provider, model string) agent.ContextWindowResolution
	WindowFor              func(model string) int
	AutoSummarizer         agent.Summarizer
	NavigatorProvider      llm.Provider
}

// wireContextWindows builds resolvers for context windows, starts a
// background /v1/models probe for provider-reported sizes, and picks
// the navigator side provider + auto-summarizer.
func wireContextWindows(
	dataDir string,
	cfg config.Config,
	tomlCfg config.TomlConfig,
	caps *llm.CapabilityRegistry,
	compactProvider llm.Provider,
	registry *tools.Registry,
	taskWorkerProvider, draftProvider llm.Provider,
) contextWindowBundle {
	// Wave 4: context-window resolution + auto-compact wiring.
	// Cascade: config context_window > provider /v1/models
	// metadata (fetched in the background, parsed defensively)
	// > learned limit (persisted from past context-length
	// errors) > 16384 default (applied inside the loop).
	var b contextWindowBundle
	b.Learned = loadLearnedLimits(dataDir)
	b.ModelContexts = config.LoadModelContextStore(dataDir)
	b.PrefillProfiles = llm.LoadPrefillProfiles(dataDir)
	b.InitialContextProvider = config.RuntimeProviderName(tomlCfg, cfg)

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
	b.ContextWindowFor = func(model string) agent.ContextWindowResolution {
		provWinMu.Lock()
		w := provWindows[model]
		provWinMu.Unlock()
		return agent.ResolveContextWindow(model, tomlCfg.ContextWindow, w, caps, b.Learned, cfg.BaseURL)
	}
	b.ScopedContextWindowFor = func(provider, model string) agent.ContextWindowResolution {
		if tokens, ok := b.ModelContexts.Get(provider, model); ok {
			return agent.ContextWindowResolution{Tokens: tokens, Source: "model-override"}
		}
		return agent.ContextWindowResolution{}
	}
	// Legacy helpers such as resume sizing need only the numeric value; keep
	// them on the same resolver rather than duplicating the cascade.
	b.WindowFor = func(model string) int { return b.ContextWindowFor(model).Tokens }
	// The shared helper keeps TUI, batch and WebGUI behaviour identical,
	// including falling back to the active model when compact_model is down.
	b.AutoSummarizer = agent.NewAutoSummarizerWithProvider(compactProvider, registry.ActiveNames)

	// The navigator's route classification is a tiny call, but on the
	// main provider it thrashes a single-slot llama.cpp KV cache (its
	// prompt is a different prefix, so the coordinator re-prefills on
	// the next call). When a small side provider is already configured,
	// reuse it — no new knob: task_model's worker host first (a
	// dedicated second host, so the main slot is never touched), then
	// the draft provider. Nil keeps today's behaviour (main provider).
	b.NavigatorProvider = resolveNavigatorProvider(taskWorkerProvider, draftProvider)
	return b
}
