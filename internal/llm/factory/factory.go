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
	build   BuildFunc
	dataDir string
	caps    *llm.CapabilityRegistry
	sink    llm.CallSink
}

// New returns a Factory over build (nil = Default). sinks receive one
// CallStat per model call, fanned out via llm.MultiSink; with no
// usable sink the Factory still wraps with a no-op sink so the
// gate/preemption semantics of llm.Metered always apply.
func New(build BuildFunc, dataDir string, caps *llm.CapabilityRegistry, sinks ...llm.CallSink) *Factory {
	if build == nil {
		build = Default
	}
	sink := llm.MultiSink(sinks...)
	if sink == nil {
		sink = func(llm.CallStat) {}
	}
	return &Factory{build: build, dataDir: dataDir, caps: caps, sink: sink}
}

// Build constructs the provider for cfg and wraps it in llm.Metered
// with the given default purpose label. An already-metered provider
// (a BuildFunc composing another factory) is returned as-is — never
// double-wrapped: nesting would double-report every call and deadlock
// the background gate.
func (f *Factory) Build(cfg config.Config, purpose string) (llm.Provider, error) {
	p, err := f.build(cfg, f.dataDir, f.caps)
	if err != nil || p == nil {
		return p, err
	}
	if llm.IsMetered(p) {
		return p, nil
	}
	return llm.Metered(p, cfg.Provider, purpose, f.sink), nil
}

// Default maps a config to a concrete raw llm.Provider: echo,
// responses, opencode, codex, anthropic, or OpenAI-compatible.
// (Moved verbatim from the web GUI's private buildProviderWithDataDir
// so both front-ends share one construction table.)
func Default(cfg config.Config, dataDir string, caps *llm.CapabilityRegistry) (llm.Provider, error) {
	if cfg.IsEcho() {
		return llm.NewEcho(cfg.Model)
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
			Timeout:      cfg.Timeout,
			Capabilities: caps,
		})
	}
}

func buildCodexProvider(cfg config.Config, dataDir string, caps *llm.CapabilityRegistry) (llm.Provider, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("codex provider requires SuperCli data dir")
	}
	labels, _ := codexauth.ListAccounts(dataDir)
	var logged []string
	for _, label := range labels {
		mgr := codexauth.NewManagerFor(dataDir, label, codexauth.Options{BackendURL: cfg.BaseURL})
		if mgr.LoggedIn() {
			logged = append(logged, label)
		}
	}
	if len(logged) == 0 {
		mgr := codexauth.NewManager(dataDir, codexauth.Options{BackendURL: cfg.BaseURL})
		if !mgr.LoggedIn() {
			return nil, fmt.Errorf("codex: not logged in — run /login in TUI first")
		}
		logged = []string{codexauth.DefaultAccount}
	}
	pool := make([]llm.Provider, 0, len(logged))
	for _, label := range logged {
		mgr := codexauth.NewManagerFor(dataDir, label, codexauth.Options{BackendURL: cfg.BaseURL})
		info, _ := mgr.Account()
		p, err := llm.NewCodex(llm.CodexConfig{
			BackendURL:   mgr.Options().BackendURL,
			Model:        cfg.Model,
			Tokens:       mgr,
			Timeout:      cfg.Timeout,
			Capabilities: caps,
			DataDir:      dataDir,
			AccountID:    info.AccountID,
		})
		if err != nil {
			return nil, err
		}
		pool = append(pool, p)
	}
	if len(pool) == 1 {
		return pool[0], nil
	}
	return llm.NewRouter(pool...)
}
