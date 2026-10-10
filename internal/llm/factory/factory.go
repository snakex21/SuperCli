// Package factory is the ONE place providers are born. Every
// front-end (CLI/TUI, batch, web GUI) builds its llm.Provider through
// a Factory, and the Factory guarantees the result is wrapped in
// llm.Metered — so no model call in the process can bypass the
// purpose-labeled call ledger, the background gate, or foreground
// preemption. Historically each call site wrapped (or forgot to wrap)
// by hand: /council members, consult samples and the web GUI all
// leaked raw providers whose WithPurpose/WithBackground marks were
// silently ignored.
package factory

import (
	"context"
	"fmt"
	"sync"

	"supercli/internal/account/codexauth"
	"supercli/internal/llm"
	"supercli/internal/system/config"
)

// BuildFunc constructs a RAW transport provider for cfg. Front-ends
// with extra construction concerns (the CLI's codex account pool,
// Kilo IP shuffler, opencode discovery logging) supply their own;
// Default covers the standard transports.
type BuildFunc func(cfg config.Config, dataDir string, caps *llm.CapabilityRegistry) (llm.Provider, error)

// Factory builds metered providers. The zero value is not usable;
// construct with New.
type Factory struct {
	build      BuildFunc
	dataDir    string
	caps       *llm.CapabilityRegistry
	sink       llm.CallSink
	useDefault bool
	mu         sync.RWMutex
	codexOpts  *codexauth.Options
}

// New returns a Factory over build (nil = Default). sinks receive one
// CallStat per model call, fanned out via llm.MultiSink; with no
// usable sink the Factory still wraps with a no-op sink so the
// gate/preemption semantics of llm.Metered always apply.
func New(build BuildFunc, dataDir string, caps *llm.CapabilityRegistry, sinks ...llm.CallSink) *Factory {
	useDefault := build == nil
	if build == nil {
		build = Default
	}
	sink := llm.MultiSink(sinks...)
	if sink == nil {
		sink = func(llm.CallStat) {}
	}
	return &Factory{build: build, dataDir: dataDir, caps: caps, sink: sink, useDefault: useDefault}
}

// SetCodexAuthOptions supplies project-resolved OAuth options to the default
// factory. Custom CLI builders retain their own manager/configuration contract.
func (f *Factory) SetCodexAuthOptions(opts codexauth.Options) {
	opts = opts.WithDefaults()
	f.mu.Lock()
	f.codexOpts = &opts
	f.mu.Unlock()
}

// Build constructs the provider for cfg and wraps it in llm.Metered
// with the given default purpose label. An already-metered provider
// (a BuildFunc composing another factory) is returned as-is — never
// double-wrapped: nesting would double-report every call and deadlock
// the background gate.
func (f *Factory) Build(cfg config.Config, purpose string) (llm.Provider, error) {
	var p llm.Provider
	var err error
	f.mu.RLock()
	opts := f.codexOpts
	f.mu.RUnlock()
	if f.useDefault && cfg.Provider == config.ProviderCodex && opts != nil {
		p, err = buildCodexProviderWithAuth(cfg, f.dataDir, f.caps, *opts)
	} else {
		p, err = f.build(cfg, f.dataDir, f.caps)
	}
	if err != nil || p == nil {
		return p, err
	}
	if llm.IsMetered(p) {
		return p, nil
	}
	return llm.MeteredConnection(p, cfg.Provider, cfg.BaseURL, cfg.APIKey, purpose, f.sink), nil
}

// Default maps a config to a concrete raw llm.Provider: echo,
// responses, opencode, codex, anthropic, or OpenAI-compatible.
// (Moved verbatim from the web GUI's private buildProviderWithDataDir
// so both front-ends share one construction table.)
func Default(cfg config.Config, dataDir string, caps *llm.CapabilityRegistry) (llm.Provider, error) {
	// Provider discovery normally populates capabilities, but a saved local
	// model can be used before /api/models is ever opened. Ensure name-based
	// multimodal families (notably Qwen 3.5 community builds) are available to
	// the transport at construction time, otherwise it strips direct images.
	if caps != nil && cfg.Model != "" {
		heuristic := llm.HeuristicCapabilities(cfg.Model)
		if existing, ok := caps.Get(cfg.Model); ok {
			if existing.Source != llm.SourceCatalog && existing.Source != llm.SourceProbe {
				if !existing.VisionKnown {
					existing.Vision = existing.Vision || heuristic.Vision
				}
				if !existing.ReasoningKnown {
					existing.Reasoning = existing.Reasoning || heuristic.Reasoning
				}
				existing.Stream = existing.Stream || heuristic.Stream
				caps.Register(existing)
			}
		} else {
			caps.Register(heuristic)
		}
	}
	if cfg.IsEcho() {
		return llm.NewEcho(cfg.Model)
	}
	zenInfo, zenModel := llm.ResolveOpenCodeZenModelMetadata(dataDir, cfg.BaseURL, cfg.Model, caps)
	// Zen free-tier transport is catalog-driven (models.dev npm → endpoint).
	// Never let cfg.Provider force the wrong dialect: openai-compatible free
	// models must not land on /responses (500), muse must not land on chat.
	// Unknown Zen models default to OpenAI chat + gate tools — that is the
	// majority shape for new free-tier releases until the catalog catches up.
	if zenModel || llm.IsOpenCodeZenBaseURL(cfg.BaseURL) {
		switch {
		case zenModel && zenInfo.Transport == llm.ModelTransportResponses:
			return llm.NewResponses(llm.ResponsesConfig{
				BaseURL:        cfg.BaseURL,
				APIKey:         cfg.APIKey,
				Model:          cfg.Model,
				Timeout:        cfg.Timeout,
				ConnectTimeout: cfg.ConnectTimeout,
				Capabilities:   caps,
			})
		case zenModel && zenInfo.Transport == llm.ModelTransportAnthropic:
			return llm.NewAnthropic(llm.AnthropicConfig{
				BaseURL:        cfg.BaseURL,
				APIKey:         cfg.APIKey,
				Model:          cfg.Model,
				MaxTokens:      cfg.MaxTokens,
				Timeout:        cfg.Timeout,
				ConnectTimeout: cfg.ConnectTimeout,
				Capabilities:   caps,
			})
		case zenModel && zenInfo.Transport == llm.ModelTransportGoogle:
			return nil, fmt.Errorf("opencode Zen model %q requires Google transport, which this engine does not support yet", cfg.Model)
		default:
			// openai-compatible (catalog) or unknown Zen model → chat/completions.
			return llm.NewOpenAI(llm.OpenAIConfig{
				BaseURL:      cfg.BaseURL,
				APIKey:       cfg.APIKey,
				Model:        cfg.Model,
				MaxTokens:    cfg.MaxTokens,
				Timeout:      cfg.Timeout,
				Capabilities: caps,
			})
		}
	}
	if protocol := llm.AnyRouterModelProtocol(cfg.BaseURL, cfg.Model); protocol != "" {
		cfg.Provider = protocol
		cfg.BaseURL = llm.NormalizeAnyRouterBaseURL(cfg.BaseURL)
	}
	switch cfg.Provider {
	case config.ProviderResponses:
		return llm.NewResponses(llm.ResponsesConfig{
			BaseURL:        cfg.BaseURL,
			APIKey:         cfg.APIKey,
			Model:          cfg.Model,
			Timeout:        cfg.Timeout,
			ConnectTimeout: cfg.ConnectTimeout,
			Capabilities:   caps,
		})
	case config.ProviderOpencode:
		p, err := llm.NewOpencode(llm.OpencodeConfig{
			BaseURL:      cfg.BaseURL,
			APIKey:       cfg.APIKey,
			Model:        cfg.Model,
			MaxTokens:    cfg.MaxTokens,
			Capabilities: caps,
		})
		if err != nil {
			return nil, fmt.Errorf("opencode: %w", err)
		}
		// Best-effort model discovery; gateway being down is not fatal.
		_, _ = p.ProbeModels(context.Background())
		return p, nil
	case config.ProviderCodex:
		return buildCodexProvider(cfg, dataDir, caps)
	case config.ProviderAnthropic:
		return llm.NewAnthropic(llm.AnthropicConfig{
			BaseURL:      cfg.BaseURL,
			APIKey:       cfg.APIKey,
			Model:        cfg.Model,
			MaxTokens:    cfg.MaxTokens,
			Timeout:      cfg.Timeout,
			Capabilities: caps,
		})
	default:
		return llm.NewOpenAI(llm.OpenAIConfig{
			BaseURL:      cfg.BaseURL,
			APIKey:       cfg.APIKey,
			Model:        cfg.Model,
			MaxTokens:    cfg.MaxTokens,
			Timeout:      cfg.Timeout,
			Capabilities: caps,
		})
	}
}

func buildCodexProvider(cfg config.Config, dataDir string, caps *llm.CapabilityRegistry) (llm.Provider, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("codex provider requires SuperCli data dir")
	}
	opts := codexauth.Options{BackendURL: cfg.BaseURL}
	// A front-end may use the default factory without the CLI global manager.
	// Resolve the same portable OAuth configuration for all named accounts.
	if tc, err := config.ResolveConfig(dataDir, ".", ""); err == nil {
		opts.ClientID, opts.Issuer = tc.CodexAuth.ClientID, tc.CodexAuth.Issuer
		if opts.BackendURL == "" {
			opts.BackendURL = tc.CodexAuth.BackendURL
		}
	}
	opts = opts.WithDefaults()
	return buildCodexProviderWithAuth(cfg, dataDir, caps, opts)
}

func buildCodexProviderWithAuth(cfg config.Config, dataDir string, caps *llm.CapabilityRegistry, opts codexauth.Options) (llm.Provider, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("codex provider requires SuperCli data dir")
	}
	if cfg.BaseURL != "" {
		opts.BackendURL = cfg.BaseURL
	}
	opts = opts.WithDefaults()
	labels, _ := codexauth.ListAccounts(dataDir)
	var logged []string
	hasLogin := false
	for _, label := range labels {
		mgr := codexauth.NewManagerFor(dataDir, label, opts)
		if mgr.LoggedIn() {
			hasLogin = true
			if identity, err := mgr.CatalogIdentity(); err == nil {
				if available, known := llm.CodexModelCacheAvailability(dataDir, opts.BackendURL, identity, cfg.Model); known && !available {
					continue
				}
			}
			logged = append(logged, label)
		}
	}
	if len(logged) == 0 {
		if hasLogin {
			return nil, fmt.Errorf("codex: selected model is absent from the logged-in accounts' current catalogs")
		}
		mgr := codexauth.NewManager(dataDir, opts)
		if !mgr.LoggedIn() {
			return nil, fmt.Errorf("codex: not logged in — run /login in TUI first")
		}
		logged = []string{codexauth.DefaultAccount}
	}
	pool := make([]llm.Provider, 0, len(logged))
	for _, label := range logged {
		mgr := codexauth.NewManagerFor(dataDir, label, opts)
		info, _ := mgr.Account()
		p, err := llm.NewCodex(llm.CodexConfig{
			BackendURL:     mgr.Options().BackendURL,
			Model:          cfg.Model,
			Tokens:         mgr,
			Timeout:        cfg.Timeout,
			ConnectTimeout: cfg.ConnectTimeout,
			Capabilities:   caps,
			DataDir:        dataDir,
			AccountID:      info.AccountID,
		})
		if err != nil {
			return nil, err
		}
		pool = append(pool, p)
	}
	if len(pool) == 1 {
		return pool[0], nil
	}
	router, err := llm.NewRouter(pool...)
	if err == nil {
		router.SetLabels(logged)
	}
	return router, err
}
