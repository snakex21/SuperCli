// Package providers manages the provider list stored in
// config.toml. It provides CRUD operations on providers,
// connectivity probing, model visibility toggling, and
// manual price editing.
package providers

import (
	"encoding/base64"
	"sort"
	"strings"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

type ModelRef struct {
	Provider string
	ID       string
}

func hiddenKey(provider, modelID string) string {
	return strings.TrimSpace(provider) + "\x00" + strings.TrimSpace(modelID)
}

// IsHiddenFor reports visibility for one provider/model pair.
func (m *Manager) IsHiddenFor(provider, modelID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.hidden[hiddenKey(provider, modelID)]
	return ok
}

// IsHidden is the legacy unscoped form kept for programmatic compatibility.
func (m *Manager) IsHidden(modelID string) bool { return m.IsHiddenFor("", modelID) }

// ToggleHiddenFor flips visibility for one provider/model pair.
func (m *Manager) ToggleHiddenFor(provider, modelID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := hiddenKey(provider, modelID)
	if _, ok := m.hidden[key]; ok {
		delete(m.hidden, key)
		m.saveHiddenLocked()
		return false
	}
	m.hidden[key] = struct{}{}
	m.saveHiddenLocked()
	return true
}

// ToggleHidden is the legacy unscoped form.
func (m *Manager) ToggleHidden(modelID string) bool { return m.ToggleHiddenFor("", modelID) }

// SetModelsHidden applies one visibility state to a group of model IDs and
// persists the hidden set once. This is intentionally a batch operation: a
// provider catalog can contain hundreds of models and should not cause one
// config rewrite (or one HTTP request) per row.
func (m *Manager) SetModelsHidden(modelIDs []string, hidden bool) int {
	refs := make([]ModelRef, 0, len(modelIDs))
	for _, id := range modelIDs {
		refs = append(refs, ModelRef{ID: id})
	}
	return m.SetModelRefsHidden(refs, hidden)
}

// SetModelRefsHidden applies one visibility state to provider-scoped models
// and persists once, even for a large filtered catalog.
func (m *Manager) SetModelRefsHidden(refs []ModelRef, hidden bool) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	changed := 0
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		id := strings.TrimSpace(ref.ID)
		if id == "" {
			continue
		}
		key := hiddenKey(ref.Provider, id)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		_, alreadyHidden := m.hidden[key]
		if hidden && !alreadyHidden {
			m.hidden[key] = struct{}{}
			changed++
		} else if !hidden && alreadyHidden {
			delete(m.hidden, key)
			changed++
		}
	}
	if changed > 0 {
		m.saveHiddenLocked()
	}
	return changed
}

// ShowModel ensures a model is visible (removes it from the
// hidden set if present). Returns true if the model was
// previously hidden and is now visible.
func (m *Manager) ShowModelFor(provider, modelID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := hiddenKey(provider, modelID)
	if _, ok := m.hidden[key]; ok {
		delete(m.hidden, key)
		m.saveHiddenLocked()
		return true
	}
	return false
}

func (m *Manager) ShowModel(modelID string) bool { return m.ShowModelFor("", modelID) }

// HideModel hides a model (adds it to the hidden set).
// Returns true if the model was previously visible.
func (m *Manager) HideModelFor(provider, modelID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := hiddenKey(provider, modelID)
	if _, ok := m.hidden[key]; !ok {
		m.hidden[key] = struct{}{}
		m.saveHiddenLocked()
		return true
	}
	return false
}

func (m *Manager) HideModel(modelID string) bool { return m.HideModelFor("", modelID) }

// LoadHiddenState loads the list of hidden model IDs from
// config.toml into the in-memory hidden map.
func (m *Manager) LoadHiddenState() {
	m.mu.Lock()
	defer m.mu.Unlock()

	tc, err := config.LoadToml(m.tomlPath)
	if err != nil {
		return
	}
	m.hidden = make(map[string]struct{})
	migrated := false
	for _, raw := range tc.HiddenModels {
		if key, ok := decodeHiddenRef(raw); ok {
			m.hidden[key] = struct{}{}
			continue
		}
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		migrated = true
		if len(m.providers) == 0 {
			m.hidden[hiddenKey("", id)] = struct{}{}
			continue
		}
		// Legacy entries were global, so preserve their old effect for every
		// provider that existed at migration time. Future providers remain
		// independent.
		for _, p := range m.providers {
			m.hidden[hiddenKey(p.Name, id)] = struct{}{}
		}
	}
	if migrated {
		m.saveHiddenLocked()
	}
}

// saveHiddenLocked writes the hidden models set to
// config.toml. Must be called with m.mu held.
func (m *Manager) saveHiddenLocked() {
	tc, err := config.LoadToml(m.tomlPath)
	if err != nil {
		tc = config.TomlConfig{}
	}
	ids := make([]string, 0, len(m.hidden))
	for key := range m.hidden {
		ids = append(ids, encodeHiddenRef(key))
	}
	sort.Strings(ids)
	tc.HiddenModels = ids
	config.SaveToml(m.tomlPath, tc)
}

const hiddenRefPrefix = "ref:"

func encodeHiddenRef(key string) string {
	return hiddenRefPrefix + base64.RawURLEncoding.EncodeToString([]byte(key))
}

func decodeHiddenRef(raw string) (string, bool) {
	if !strings.HasPrefix(raw, hiddenRefPrefix) {
		return "", false
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(raw, hiddenRefPrefix))
	if err != nil || !strings.ContainsRune(string(b), '\x00') {
		return "", false
	}
	return string(b), true
}

// VisibleModels returns models from caps.All() that are not hidden
// and belong to the given provider (empty provider = all).
func (m *Manager) VisibleModels(caps *llm.CapabilityRegistry, provider string) []llm.ModelInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []llm.ModelInfo
	for _, mi := range caps.All() {
		if provider != "" && mi.Provider != provider {
			continue
		}
		if provider != "" && !isDiscoveredProviderModel(mi) {
			continue
		}
		if p, ok := m.providerByNameLocked(mi.Provider); ok && !modelVisibleForProvider(p, mi.ID) {
			continue
		}
		if _, hidden := m.hidden[hiddenKey(mi.Provider, mi.ID)]; hidden {
			continue
		}
		out = append(out, mi)
	}
	return out
}

func isDiscoveredProviderModel(mi llm.ModelInfo) bool {
	return mi.Source != llm.SourceSeed
}

// ModelVisible reports whether a model should be shown for a configured
// provider. For anonymous/public OpenCode and Kilo entries we only show
// explicit free IDs; paid models often arrive without reliable pricing.
func (m *Manager) ModelVisible(provider, id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.modelVisibleLocked(provider, id)
}

// ModelCatalogVisible is the catalog counterpart of ModelVisible. A paused
// provider is not selectable in /model, but its remembered models remain in
// /models so the user can inspect them and resume the provider later.
func (m *Manager) ModelCatalogVisible(provider, id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if p, ok := m.providerByNameLocked(provider); ok {
		return modelVisibleForProvider(p, id)
	}
	return true
}

func (m *Manager) modelVisibleLocked(provider, id string) bool {
	if p, ok := m.providerByNameLocked(provider); ok {
		if p.Disabled {
			return false
		}
		return modelVisibleForProvider(p, id)
	}
	return true
}

func (m *Manager) providerByNameLocked(name string) (config.ProviderConf, bool) {
	for _, p := range m.providers {
		if p.Name == name {
			return p, true
		}
	}
	return config.ProviderConf{}, false
}

func modelVisibleForProvider(p config.ProviderConf, id string) bool {
	if !freeOnlyProvider(p) {
		return true
	}
	return llm.IsFreeModelID(id)
}

func freeOnlyProvider(p config.ProviderConf) bool {
	key := llm.KiloDefaultKey(p.BaseURL, p.APIKey)
	if strings.Contains(p.BaseURL, "api.kilo.ai") {
		return key == "anonymous"
	}
	if strings.Contains(p.BaseURL, "opencode.ai/zen") {
		return key == "public"
	}
	name := strings.ToLower(strings.ReplaceAll(p.Name, " ", ""))
	return (name == "opencode" || name == "kilocode" || name == "kilo") && p.APIKey == ""
}

// SetPrice updates a model's manual price in config.toml.
// This writes to the model_prices array with source="user".
