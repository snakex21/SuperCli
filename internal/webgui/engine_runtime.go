// Package webgui serves a local, dark-themed web GUI for SuperCli.
//
// It reuses the existing core packages (agent loop, providers,
// sessions, credits, goals, memory) through their public APIs, so
// the GUI is a real front-end over the same engine the TUI drives —
// not a mock. The server is pure net/http + embedded assets; no CGO,
// no new dependencies, keeping the single-binary, portable contract.
package webgui

import (
	"strings"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

func (e *Engine) ModelName() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e == nil || e.prov == nil {
		return ""
	}
	return e.prov.Name()
}

// RuntimeSelection returns the browser-facing provider/model/reasoning tuple
// that future turns will use. Provider is the configured provider name when it
// can be resolved, falling back to the transport type for local/legacy setups.
func (e *Engine) RuntimeSelection() (provider, model, reasoning string) {
	e.mu.RLock()
	cfg := e.cfg
	e.mu.RUnlock()
	model = cfg.Model
	for _, configured := range e.providerManager().Configured() {
		if configured.Type == cfg.Provider && strings.TrimRight(configured.BaseURL, "/") == strings.TrimRight(cfg.BaseURL, "/") {
			provider = configured.Name
			if configured.Model == model {
				break
			}
		}
	}
	if provider == "" {
		provider = e.caps.Provider(model)
	}
	if provider == "" {
		provider = cfg.Provider
	}
	return provider, model, llm.ReasoningEffort()
}

// ReasoningSupportKey returns the backend-scoped key used by the OpenAI-
// compatible provider's live effort negotiation. Native providers retain the
// model-only key used by their request builders.
func (e *Engine) ReasoningSupportKey() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.cfg.Provider == config.ProviderOpenAI || e.cfg.Provider == config.ProviderResponses || e.cfg.Provider == config.ProviderOpencode {
		model := e.cfg.Model
		if e.cfg.Provider == config.ProviderOpencode {
			model = strings.TrimPrefix(model, "opencode/")
		}
		return llm.ReasoningSupportKey(e.cfg.BaseURL, model)
	}
	return e.cfg.Model
}

// SupportsUnifiedReasoningGateway reports whether the active OpenAI-compatible
// endpoint performs its own reasoning-effort mapping (Kilo/OpenRouter/OpenCode).
func (e *Engine) SupportsUnifiedReasoningGateway() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.cfg.Provider != config.ProviderOpenAI && e.cfg.Provider != config.ProviderOpencode {
		return false
	}
	return llm.IsUnifiedReasoningGateway(e.cfg.BaseURL)
}

// Home returns the file-sandbox root.
func (e *Engine) Home() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.home
}

// setHome updates the file-sandbox root for future web requests. Web runs
// build a fresh agent loop per /api/chat request, so unlike the TUI this can
// take effect immediately for subsequent chats and file-browser calls.
func (e *Engine) setHome(home string) {
	e.mu.Lock()
	e.home = home
	e.mu.Unlock()
	// Project config may add or override MCP servers. Refresh metadata now;
	// no process is started until a later bridge call.
	_ = e.reloadMCP()
}

// DataDir returns the SuperCli data directory.
func (e *Engine) DataDir() string { return e.dataDir }

// SetAppProfile applies behavior owned by a branded front-end without
// changing SuperCli defaults. The profile is fixed by the launcher before the
// first request; the lock keeps tests and future embedded callers safe.
func (e *Engine) SetAppProfile(profile string) {
	e.mu.Lock()
	e.appProfile = strings.ToLower(strings.TrimSpace(profile))
	e.mu.Unlock()
}

// newLoop builds a fresh agent loop with the standard always-on file
// tool set. Each web run gets its own loop so concurrent browser tabs
// do not share mutable conversation state. Mirrors runBatch's
// registry so the web agent can actually read and edit files.
