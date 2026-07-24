// Package providers manages the provider list stored in
// config.toml. It provides CRUD operations on providers,
// connectivity probing, model visibility toggling, and
// manual price editing.
package providers

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

// Manager reads/writes the providers list from config.toml
// and maintains in-memory model visibility state.
type Manager struct {
	mu       sync.RWMutex
	home     string
	tomlPath string // full path to the GLOBAL config.toml
	// activePath is where the runtime model selection
	// (default_model / default_provider) is persisted. It
	// defaults to the global config, but main.go points it at
	// the project config (<cwd>/.supercli/config.toml) when one
	// is in effect — otherwise a /model swap saved to the global
	// config would be silently shadowed at startup by a project
	// config that resolution merges with higher priority.
	activePath string
	providers  []config.ProviderConf
	// hidden tracks models whose visibility is toggled off.
	// Key: model ID. Value: true = hidden.
	hidden map[string]struct{}
}

// NewManager creates a Manager that reads providers from
// the global config.toml at <dataDir>/config.toml.
func NewManager(dataDir string) *Manager {
	global, _ := config.FindTomlPaths(dataDir, ".")
	return &Manager{
		home:       dataDir,
		tomlPath:   global,
		activePath: global,
		hidden:     make(map[string]struct{}),
	}
}

// SetActiveConfigPath sets the config.toml that the runtime model
// selection (default_model / default_provider) is persisted to and
// read from. main.go points this at the highest-priority config in
// effect (the project config when present, otherwise the global
// config) so a /model swap survives a restart. An empty path is
// ignored, keeping the default (global) path.
func (m *Manager) SetActiveConfigPath(path string) {
	if path == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activePath = path
}

// ProviderInfo is the public view of a provider for display.
type ProviderInfo struct {
	Name      string
	Type      string
	BaseURL   string
	Model     string // default model configured for this provider
	HasKey    bool   // true if an API key is configured (key value never exposed)
	Disabled  bool   // saved but excluded from scans and active model selection
	Connected bool
	Error     string
	Models    []llm.ModelInfo
}

// ScanResult reports the outcome of checking one provider's /v1/models
// endpoint. Err is set for invalid keys, unreachable endpoints, bad JSON,
// and non-2xx statuses; in those cases no models are registered.
type ScanResult struct {
	Provider string
	Models   []string
	Err      error
}

// List returns all configured providers with connectivity status.
// Models come from the capability registry filtered by provider name.
func (m *Manager) List(caps *llm.CapabilityRegistry) []ProviderInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []ProviderInfo
	for _, p := range m.providers {
		pi := ProviderInfo{
			Name:     p.Name,
			Type:     p.Type,
			BaseURL:  p.BaseURL,
			Disabled: p.Disabled,
		}
		if p.Disabled {
			out = append(out, pi)
			continue
		}
		// Probe connectivity.
		connected, err := probeProvider(p)
		pi.Connected = connected
		if err != nil {
			pi.Error = err.Error()
		}
		// Filter models by provider. Do not show embedded seed
		// entries for configured providers: the provider scanner
		// must prove the API key works and report real models.
		for _, mi := range caps.All() {
			if mi.Provider == p.Name && isDiscoveredProviderModel(mi) && modelVisibleForProvider(p, mi.ID) {
				pi.Models = append(pi.Models, mi)
			}
		}
		out = append(out, pi)
	}
	return out
}

// ListConfigured returns all configured providers WITHOUT
// probing connectivity — cheap enough to call from a TUI render
// path. Connected is always false; callers overlay live status
// from their own async probes. Models come from the capability
// registry filtered by provider name.
func (m *Manager) ListConfigured(caps *llm.CapabilityRegistry) []ProviderInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []ProviderInfo
	for _, p := range m.providers {
		pi := ProviderInfo{
			Name:     p.Name,
			Type:     p.Type,
			BaseURL:  p.BaseURL,
			Model:    p.Model,
			HasKey:   hasExplicitProviderKey(p),
			Disabled: p.Disabled,
		}
		seen := make(map[string]struct{})
		if caps != nil {
			for _, mi := range caps.All() {
				if mi.Provider == p.Name && isDiscoveredProviderModel(mi) && modelVisibleForProvider(p, mi.ID) {
					pi.Models = append(pi.Models, mi)
					seen[mi.ID] = struct{}{}
				}
			}
		}
		known := append([]string(nil), p.CachedModels...)
		if p.Model != "" {
			known = append(known, p.Model)
		}
		for _, id := range known {
			id = strings.TrimSpace(id)
			if id == "" || !modelVisibleForProvider(p, id) {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			mi := llm.HeuristicCapabilities(id)
			if caps != nil {
				if cached, ok := caps.Get(id); ok {
					mi = cached
				}
			}
			mi.ID = id
			mi.Provider = p.Name
			mi.Source = llm.SourceProvider
			pi.Models = append(pi.Models, mi)
			seen[id] = struct{}{}
		}
		sort.Slice(pi.Models, func(i, j int) bool { return pi.Models[i].ID < pi.Models[j].ID })
		out = append(out, pi)
	}
	return out
}

// SetDisabled keeps a provider's credentials and cached model metadata while
// removing it from scans and active selection. This is intended for local or
// remote self-hosted servers that are only online occasionally.
func (m *Manager) SetDisabled(name string, disabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.providers {
		if m.providers[i].Name == name {
			m.providers[i].Disabled = disabled
			return m.saveLocked()
		}
	}
	return fmt.Errorf("provider %q not found", name)
}

// IsDisabled reports the saved availability switch without probing or IO.
func (m *Manager) IsDisabled(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.providerByNameLocked(name)
	return ok && p.Disabled
}

// Add appends a new provider to the config.toml list.
func (m *Manager) Add(name, typ, baseURL, apiKey, model string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("provider name is required")
	}

	// Check for duplicate name.
	for _, p := range m.providers {
		if p.Name == name {
			return fmt.Errorf("provider %q already exists", name)
		}
	}

	p := config.ProviderConf{
		Name:    name,
		Type:    typ,
		BaseURL: baseURL,
		// Store only a key the user actually supplied. Public gateway
		// placeholders ("anonymous"/"public") are injected at request time,
		// otherwise the UI misleadingly claims that a key is configured.
		APIKey: llm.CleanAPIKey(apiKey),
		Model:  model,
	}
	m.providers = append(m.providers, p)
	err := m.saveLocked()
	if err == nil {
		llm.InvalidateProviderModelCache(baseURL)
	}
	return err
}

// Remove removes a provider by name from the config.toml list.
func (m *Manager) Remove(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	found := false
	removedBaseURL := ""
	var filtered []config.ProviderConf
	for _, p := range m.providers {
		if p.Name == name {
			found = true
			removedBaseURL = p.BaseURL
			continue
		}
		filtered = append(filtered, p)
	}
	if !found {
		return fmt.Errorf("provider %q not found", name)
	}
	m.providers = filtered
	err := m.saveLocked()
	if err == nil {
		llm.InvalidateProviderModelCache(removedBaseURL)
	}
	return err
}

// Update modifies an existing provider's fields. Only non-nil pointers are
// applied; an explicit pointer to "" clears that field.
func (m *Manager) Update(name string, typ, baseURL, apiKey, model *string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, p := range m.providers {
		if p.Name == name {
			oldBaseURL := p.BaseURL
			if apiKey != nil && *apiKey != "" && llm.CleanAPIKey(*apiKey) == "" {
				return errors.New("API key is empty after removing whitespace and wrappers")
			}
			if typ != nil {
				m.providers[i].Type = *typ
			}
			if baseURL != nil {
				m.providers[i].BaseURL = *baseURL
			}
			if apiKey != nil {
				m.providers[i].APIKey = llm.CleanAPIKey(*apiKey)
			}
			if model != nil {
				m.providers[i].Model = *model
			}
			err := m.saveLocked()
			if err == nil {
				llm.InvalidateProviderModelCache(oldBaseURL)
				llm.InvalidateProviderModelCache(m.providers[i].BaseURL)
			}
			return err
		}
	}
	return fmt.Errorf("provider %q not found", name)
}

// ModelRef identifies one model at one provider. Model IDs are not globally
// unique: the same ID may be served by several independent endpoints.
