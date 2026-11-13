package providers

import (
	"context"
	"fmt"
	"strings"

	"supercli/internal/account/claudeauth"
	"supercli/internal/llm"
	"supercli/internal/system/config"
)

// ClaudeLoggedIn checks saved credentials locally without refreshing tokens.
func (m *Manager) ClaudeLoggedIn() bool {
	return claudeauth.NewManager(m.home, claudeauth.Options{}).LoggedIn()
}

// EnsureClaudeProvider registers a saved login without changing the active
// model, overwriting another provider, or copying tokens into config.toml.
func (m *Manager) EnsureClaudeProvider() (string, error) {
	if !m.ClaudeLoggedIn() {
		return "", nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	used := make(map[string]bool)
	for _, p := range m.providers {
		if p.Type == config.ProviderClaude {
			return p.Name, nil
		}
		used[strings.ToLower(p.Name)] = true
	}
	name := "claude"
	for suffix := 2; used[strings.ToLower(name)]; suffix++ {
		name = fmt.Sprintf("claude-%d", suffix)
	}
	m.providers = append(m.providers, config.ProviderConf{Name: name, Type: config.ProviderClaude, BaseURL: claudeauth.BaseURL})
	if err := m.saveLocked(); err != nil {
		m.providers = m.providers[:len(m.providers)-1]
		return "", err
	}
	return name, nil
}

func (m *Manager) scanClaudeProvider(p config.ProviderConf, caps *llm.CapabilityRegistry) ScanResult {
	res := ScanResult{Provider: p.Name}
	if strings.TrimRight(p.BaseURL, "/") != claudeauth.BaseURL {
		res.Err = fmt.Errorf("Claude account login requires %s", claudeauth.BaseURL)
		return res
	}
	if caps == nil {
		res.Err = fmt.Errorf("capability registry is not available")
		return res
	}
	res.Models, res.Err = llm.ListClaudeModels(context.Background(), m.home)
	if res.Err != nil {
		return res
	}
	for _, id := range res.Models {
		mi := llm.HeuristicCapabilities(id)
		mi.Provider = p.Name
		mi.Source = llm.SourceProvider
		caps.Register(mi)
	}
	return res
}
