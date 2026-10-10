package app

import (
	"context"
	"strings"
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
	RebindConfig           func(provider string, cfg config.Config)
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
	resolveWindowForConfig := func(model string, resolvedCfg config.Config) agent.ContextWindowResolution {
		provWinMu.Lock()
		w := 0
		if resolvedCfg.BaseURL == cfg.BaseURL && resolvedCfg.APIKey == cfg.APIKey {
			w = provWindows[model]
		}
		provWinMu.Unlock()
		return agent.ResolveContextWindowWithRuntime(model, tomlCfg.ContextWindow, w, caps, b.Learned, resolvedCfg.BaseURL, resolvedCfg.APIKey)
	}
	var resolverMu sync.RWMutex
	activeCfg := cfg
	configForProvider := contextWindowConfigForProvider(dataDir, tomlCfg, cfg, b.InitialContextProvider)
	b.RebindConfig = func(provider string, next config.Config) {
		nextConfigForProvider := contextWindowConfigForProvider(dataDir, tomlCfg, next, provider)
		resolverMu.Lock()
		activeCfg, configForProvider = next, nextConfigForProvider
		resolverMu.Unlock()
	}
	b.ContextWindowFor = func(model string) agent.ContextWindowResolution {
		resolverMu.RLock()
		resolvedCfg := activeCfg
		resolverMu.RUnlock()
		return resolveWindowForConfig(model, resolvedCfg)
	}
	b.ScopedContextWindowFor = func(provider, model string) agent.ContextWindowResolution {
		resolverMu.RLock()
		scopedCfg := configForProvider(provider)
		resolverMu.RUnlock()
		if tokens, ok := b.ModelContexts.Get(provider, model); ok {
			return agent.ClampContextWindowToRuntime(agent.ContextWindowResolution{Tokens: tokens, Source: "model-override"}, scopedCfg.BaseURL, scopedCfg.APIKey, model)
		}
		resolved := resolveWindowForConfig(model, scopedCfg)
		if resolved.Tokens <= 0 {
			// Do not fall through to the coordinator's endpoint for a worker.
			return agent.ContextWindowResolution{Tokens: agent.DefaultContextWindow(), Source: "fallback"}
		}
		return resolved
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

// contextWindowConfigForProvider snapshots configured connections once. The
// loop may ask for a window every step; resolving a worker's endpoint must not
// reread configuration each time or borrow the coordinator's runtime capacity.
func contextWindowConfigForProvider(dataDir string, tc config.TomlConfig, active config.Config, activeProvider string) func(string) config.Config {
	connections := make(map[string]config.ProviderConf)
	globalPath, _ := config.FindTomlPaths(dataDir, "")
	if global, err := config.LoadToml(globalPath); err == nil {
		for _, p := range global.Providers {
			connections[p.Name] = p
		}
	}
	for _, p := range tc.Providers {
		connections[p.Name] = p
	}
	return func(provider string) config.Config {
		provider = strings.TrimSpace(provider)
		if provider == "" || provider == activeProvider {
			return active
		}
		out := active
		// Unknown identities cannot inherit another endpoint's loaded limit.
		out.Provider, out.BaseURL, out.APIKey = provider, "", ""
		if p, ok := connections[provider]; ok {
			out.Provider, out.BaseURL, out.APIKey = p.Type, p.BaseURL, p.APIKey
		}
		return out
	}
}
