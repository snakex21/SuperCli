// Package providers manages the provider list stored in
// config.toml. It provides CRUD operations on providers,
// connectivity probing, model visibility toggling, and
// manual price editing.
package providers

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

type PredefinedProvider struct {
	Name    string
	Type    string
	BaseURL string
	Desc    string
}

// PredefinedProviders returns a list of well-known providers
// that the user can pick from when adding a new provider.
// The last entry is always "custom" for manual configuration.
func PredefinedProviders() []PredefinedProvider {
	return []PredefinedProvider{
		{Name: "openai", Type: "openai", BaseURL: "https://api.openai.com/v1", Desc: "ChatGPT account or API key"},
		{Name: "anthropic", Type: config.ProviderAnthropic, BaseURL: "https://api.anthropic.com/v1", Desc: "Claude Opus 4.8, Sonnet 4.6, Haiku 4.5"},
		{Name: "google", Type: "openai", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", Desc: "Gemini 2.5, Gemini 2.0"},
		{Name: "groq", Type: "openai", BaseURL: "https://api.groq.com/openai/v1", Desc: "Fast inference (Llama, Mixtral)"},
		{Name: "together", Type: "openai", BaseURL: "https://api.together.xyz/v1", Desc: "Open-source models cloud"},
		{Name: "deepseek", Type: "openai", BaseURL: "https://api.deepseek.com/v1", Desc: "DeepSeek-V3, DeepSeek-R1"},
		{Name: "mistral", Type: "openai", BaseURL: "https://api.mistral.ai/v1", Desc: "Mistral Large, Codestral"},
		{Name: "openrouter", Type: "openai", BaseURL: "https://openrouter.ai/api/v1", Desc: "Meta-router to 200+ models"},
		{Name: "xai", Type: "openai", BaseURL: "https://api.x.ai/v1", Desc: "Grok-3, Grok-2"},
		{Name: "huggingface", Type: "openai", BaseURL: "https://api-inference.huggingface.co/v1", Desc: "HF Inference API"},
		{Name: "kilo", Type: "openai", BaseURL: "https://api.kilo.ai/api/openrouter", Desc: "Kilo AI (free models, no key)"},
		{Name: "opencode", Type: "openai", BaseURL: "https://opencode.ai/zen/v1", Desc: "OpenCode Zen (free models, no key)"},
		{Name: "lmstudio", Type: "openai", BaseURL: "http://localhost:1234/v1", Desc: "Local models via LM Studio"},
		{Name: "ollama", Type: "openai", BaseURL: "http://localhost:11434/v1", Desc: "Local models via Ollama"},
		{Name: "custom", Type: "openai", BaseURL: "", Desc: "Your own OpenAI-compatible endpoint"},
	}
}

// Reload re-reads providers from config.toml.
func (m *Manager) Reload() {
	m.mu.Lock()
	defer m.mu.Unlock()

	tc, err := config.LoadToml(m.tomlPath)
	if err != nil {
		m.providers = nil
		return
	}
	m.providers = tc.Providers
	// Hand the cached inventory to the transport layer. A provider that
	// rejects the configured model can then answer with the models it does
	// serve instead of a bare HTTP status the user cannot act on.
	for _, p := range m.providers {
		llm.RememberProviderModels(p.BaseURL, p.CachedModels)
	}
	if m.repairUnnamedProvidersLocked() {
		// Older GUI builds accepted an empty provider name. Preserve the whole
		// provider (including credentials and selected model), but give it a
		// stable hostname-derived identity so models can be grouped and selected.
		_ = m.saveLocked()
	}
}

func (m *Manager) repairUnnamedProvidersLocked() bool {
	used := make(map[string]struct{}, len(m.providers))
	for i := range m.providers {
		if name := strings.TrimSpace(m.providers[i].Name); name != "" {
			m.providers[i].Name = name
			used[strings.ToLower(name)] = struct{}{}
		}
	}
	changed := false
	for i := range m.providers {
		if strings.TrimSpace(m.providers[i].Name) != "" {
			continue
		}
		base := "provider"
		if parsed, err := url.Parse(strings.TrimSpace(m.providers[i].BaseURL)); err == nil && parsed.Hostname() != "" {
			base = strings.ToLower(parsed.Hostname())
		}
		name := base
		for suffix := 2; ; suffix++ {
			if _, exists := used[strings.ToLower(name)]; !exists {
				break
			}
			name = fmt.Sprintf("%s-%d", base, suffix)
		}
		m.providers[i].Name = name
		used[strings.ToLower(name)] = struct{}{}
		changed = true
	}
	return changed
}

// Configured returns a copy of the configured provider entries
// (no probing, no IO beyond the already-loaded state).
func (m *Manager) Configured() []config.ProviderConf {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]config.ProviderConf, len(m.providers))
	copy(out, m.providers)
	return out
}

// APIKey returns the stored key for one configured provider. It is kept as a
// narrow accessor so secret-aware callers do not need to copy or serialize the
// complete provider configuration. The web GUI exposes this only through its
// loopback-only reveal endpoint.
func (m *Manager) APIKey(name string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.providerByNameLocked(name)
	if !ok {
		return "", false
	}
	if !hasExplicitProviderKey(p) {
		return "", true
	}
	return p.APIKey, true
}

func hasExplicitProviderKey(p config.ProviderConf) bool {
	key := strings.TrimSpace(p.APIKey)
	if key == "" {
		return false
	}
	return key != llm.KiloDefaultKey(p.BaseURL, "")
}

// Ping checks one provider conf's /v1/models endpoint with a
// short timeout. Exported for /doctor and async menu probing.
func Ping(ctx context.Context, p config.ProviderConf) error {
	done := make(chan error, 1)
	go func() {
		_, err := probeProvider(p)
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Names returns the names of all configured providers
// without probing connectivity. Safe for frequent calls.
func (m *Manager) Names() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, len(m.providers))
	for i, p := range m.providers {
		names[i] = p.Name
	}
	return names
}

// SaveActiveConfig persists the selected model ID and provider
// name to config.toml so the same provider+model combination
// is restored on next startup.
//
// providerName must match a [[providers]].name entry.
// If no matching provider is found, only default_model is saved.
