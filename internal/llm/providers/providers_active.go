// Package providers manages the provider list stored in
// config.toml. It provides CRUD operations on providers,
// connectivity probing, model visibility toggling, and
// manual price editing.
package providers

import (
	"supercli/internal/system/config"
)

func (m *Manager) SaveActiveConfig(modelID, providerName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Persist the selection to the active (highest-priority) config
	// so it wins at the next startup. When that differs from the
	// global config (a project config is in effect), also mirror
	// default_model/default_provider into the global config so both
	// layers agree regardless of which one resolution ends up
	// reading first. Without this mirror a project config would
	// shadow the global save and the choice would be lost on restart.
	if err := m.writeActiveConfig(m.activePath, modelID, providerName); err != nil {
		return err
	}
	if m.activePath != m.tomlPath {
		if err := m.writeActiveConfig(m.tomlPath, modelID, providerName); err != nil {
			return err
		}
	}
	return nil
}

// writeActiveConfig sets default_model/default_provider (and keeps the
// matching provider entry's model field in sync) in the config.toml at
// path, preserving every other field. Must be called with m.mu held.
func (m *Manager) writeActiveConfig(path, modelID, providerName string) error {
	tc, err := config.LoadToml(path)
	if err != nil {
		tc = config.TomlConfig{}
	}
	tc.DefaultModel = modelID
	tc.DefaultProvider = providerName

	// Also update the matching provider entry's model field so the
	// per-provider config stays in sync. If this config layer does not
	// contain the selected provider (common for project config overriding
	// the global providers list), copy the provider entry from the manager
	// into this layer; otherwise default_provider would point at a provider
	// that cannot be resolved on next startup.
	found := false
	for i, p := range tc.Providers {
		if p.Name == providerName {
			tc.Providers[i].Model = modelID
			found = true
			break
		}
	}
	if !found && providerName != "" {
		for _, p := range m.providers {
			if p.Name == providerName {
				p.Model = modelID
				tc.Providers = append(tc.Providers, p)
				break
			}
		}
	}

	return config.SaveToml(path, tc)
}

// SaveCouncilModels persists the user's last /council
// roster to the [council] section of config.toml so it
// becomes the default for subsequent /council runs.
func (m *Manager) SaveCouncilModels(models []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	tc, err := config.LoadToml(m.tomlPath)
	if err != nil {
		tc = config.TomlConfig{}
	}
	tc.Council.Models = models
	return config.SaveToml(m.tomlPath, tc)
}

// LoadCouncilModels reads the saved /council roster from
// config.toml. Nil when not set.
func (m *Manager) LoadCouncilModels() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tc, err := config.LoadToml(m.tomlPath)
	if err != nil {
		return nil
	}
	return tc.Council.Models
}

// SaveReasoningEffort persists reasoning_effort to config.toml.
// Like SaveActiveConfig, it writes to both project and global config
// for consistency.
func (m *Manager) SaveReasoningEffort(level string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := writeReasoningEffort(m.activePath, level); err != nil {
		return err
	}
	if m.activePath != m.tomlPath {
		if err := writeReasoningEffort(m.tomlPath, level); err != nil {
			return err
		}
	}
	return nil
}

func writeReasoningEffort(path, level string) error {
	tc, err := config.LoadToml(path)
	if err != nil {
		tc = config.TomlConfig{}
	}
	tc.ReasoningEffort = level
	return config.SaveToml(path, tc)
}

// LoadActiveModel reads the last selected model from the active
// config.toml (the same layer SaveActiveConfig persists to).
// Returns empty string if not set.
func (m *Manager) LoadActiveModel() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tc, err := config.LoadToml(m.activePath)
	if err != nil {
		return ""
	}
	return tc.DefaultModel
}

// ScanModels probes /v1/models on every configured provider
// and registers discovered models into caps. Returns the
// total number of models found across all providers.
// Providers that are unreachable are silently skipped.
