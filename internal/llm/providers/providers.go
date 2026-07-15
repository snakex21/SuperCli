// Package providers manages the provider list stored in
// config.toml. It provides CRUD operations on providers,
// connectivity probing, model visibility toggling, and
// manual price editing.
package providers

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

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
func (m *Manager) SetPrice(modelID string, inputCost, outputCost float64) error {
	return m.SetProviderPrice("", modelID, inputCost, 0, outputCost)
}

// SetProviderPrice stores a provider-scoped manual price. Provider may be
// empty for backwards compatibility with legacy global model overrides.
func (m *Manager) SetProviderPrice(provider, modelID string, inputCost, cachedInputCost, outputCost float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	provider = strings.TrimSpace(provider)
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return errors.New("model is required")
	}
	if inputCost < 0 || cachedInputCost < 0 || outputCost < 0 {
		return errors.New("model prices cannot be negative")
	}

	// Update in-memory TOML config.
	tc, err := config.LoadToml(m.tomlPath)
	if err != nil {
		// No config.toml yet — create one.
		tc = config.TomlConfig{}
	}

	// Find existing entry or append.
	found := false
	for i, mp := range tc.ModelPrices {
		if mp.Provider == provider && mp.Model == modelID {
			tc.ModelPrices[i].InputCost = inputCost
			tc.ModelPrices[i].CachedInputCost = cachedInputCost
			tc.ModelPrices[i].OutputCost = outputCost
			found = true
			break
		}
	}
	if !found {
		tc.ModelPrices = append(tc.ModelPrices, config.ModelPriceConf{
			Provider:        provider,
			Model:           modelID,
			InputCost:       inputCost,
			CachedInputCost: cachedInputCost,
			OutputCost:      outputCost,
		})
	}

	return config.SaveToml(m.tomlPath, tc)
}

// RemoveProviderPrice deletes one exact provider/model manual override.
func (m *Manager) RemoveProviderPrice(provider, modelID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	provider = strings.TrimSpace(provider)
	modelID = strings.TrimSpace(modelID)
	tc, err := config.LoadToml(m.tomlPath)
	if err != nil {
		return err
	}
	filtered := tc.ModelPrices[:0]
	found := false
	for _, mp := range tc.ModelPrices {
		if mp.Provider == provider && mp.Model == modelID {
			found = true
			continue
		}
		filtered = append(filtered, mp)
	}
	if !found {
		return fmt.Errorf("manual price for %q/%q not found", provider, modelID)
	}
	tc.ModelPrices = filtered
	return config.SaveToml(m.tomlPath, tc)
}

// SetModelPrices pushes applicable user-defined prices into the capability
// registry. Provider-scoped entries only apply to matching model ownership.
func (m *Manager) SetModelPrices(caps *llm.CapabilityRegistry) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tc, err := config.LoadToml(m.tomlPath)
	if err != nil {
		return
	}
	for _, mp := range tc.ModelPrices {
		if existing, ok := caps.Get(mp.Model); ok {
			if mp.Provider != "" && existing.Provider != mp.Provider {
				continue
			}
			existing.InputCost = mp.InputCost
			existing.OutputCost = mp.OutputCost
			existing.Source = llm.SourceUser
			caps.Register(existing)
		}
	}
}

// probeProvider checks if a provider is reachable by hitting
// its /v1/models (or /models if BaseURL already ends with /v1)
// endpoint with a short timeout.
func probeProvider(p config.ProviderConf) (bool, error) {
	if p.BaseURL == "" {
		return false, fmt.Errorf("no base URL configured")
	}
	base := strings.TrimRight(p.BaseURL, "/")
	if p.Type == config.ProviderAnthropic {
		// Anthropic base URLs may be pasted as the full ".../v1/messages"
		// endpoint; normalize back to the version root so the probe hits
		// ".../v1/models" rather than ".../v1/messages/v1/models".
		base = llm.NormalizeAnthropicBaseURL(p.BaseURL)
	}
	var url string
	if strings.HasSuffix(base, "/v1") {
		url = base + "/models"
	} else if strings.Contains(base, "api.kilo.ai") {
		// Kilo uses OpenRouter API without /v1 prefix.
		url = base + "/models"
	} else {
		url = base + "/v1/models"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return false, err
	}
	if p.Type == config.ProviderAnthropic {
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	if key := llm.CleanAPIKey(p.APIKey); key != "" {
		if p.Type == config.ProviderAnthropic {
			req.Header.Set("x-api-key", key)
		} else {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return false, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return true, nil
}

// saveLocked writes the current providers list back to config.toml.
// Must be called with m.mu held.
func (m *Manager) saveLocked() error {
	tc, err := config.LoadToml(m.tomlPath)
	if err != nil {
		tc = config.TomlConfig{}
	}
	tc.Providers = m.providers
	return config.SaveToml(m.tomlPath, tc)
}

// PredefinedProvider is a template for a well-known AI provider.
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
func (m *Manager) ScanModels(caps *llm.CapabilityRegistry) int {
	m.mu.RLock()
	providers := make([]config.ProviderConf, len(m.providers))
	copy(providers, m.providers)
	m.mu.RUnlock()

	total := 0
	for _, p := range providers {
		if p.Disabled {
			continue
		}
		res := scanProviderConf(p, caps)
		if res.Err == nil {
			total += len(res.Models)
			m.cacheProviderModels(p.Name, res.Models)
		}
	}
	return total
}

// ScanProvider checks a single configured provider by name, validates its
// API key via /v1/models, and registers only the models returned by that
// endpoint. This is used immediately after Add/Edit so the user gets real
// feedback instead of seeing embedded seed models.
func (m *Manager) ScanProvider(name string, caps *llm.CapabilityRegistry) ScanResult {
	m.mu.RLock()
	var found config.ProviderConf
	ok := false
	for _, p := range m.providers {
		if p.Name == name {
			found = p
			ok = true
			break
		}
	}
	m.mu.RUnlock()
	if !ok {
		return ScanResult{Provider: name, Err: fmt.Errorf("provider %q not found", name)}
	}
	if found.Disabled {
		return ScanResult{Provider: name, Err: fmt.Errorf("provider %q is disabled", name)}
	}
	res := scanProviderConf(found, caps)
	if res.Err == nil {
		m.cacheProviderModels(found.Name, res.Models)
	}
	return res
}

// cacheProviderModels persists only the compact model-id inventory. Detailed
// capabilities stay in the registry; this list keeps an offline local or
// remote server from disappearing from the picker after a restart.
func (m *Manager) cacheProviderModels(name string, ids []string) {
	normalized := normalizeModelIDs(ids)
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.providers {
		if m.providers[i].Name != name {
			continue
		}
		if stringSlicesEqual(m.providers[i].CachedModels, normalized) {
			return
		}
		m.providers[i].CachedModels = normalized
		_ = m.saveLocked()
		return
	}
}

func normalizeModelIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func scanProviderConf(p config.ProviderConf, caps *llm.CapabilityRegistry) ScanResult {
	res := ScanResult{Provider: p.Name}
	if caps != nil && p.Type == config.ProviderCodex {
		// ChatGPT-OAuth (codex) backend has no /v1/models
		// endpoint — register the static Codex catalog under
		// this provider entry's name instead of probing.
		res.Models = llm.RegisterCodexCatalog(caps, p.Name)
		return res
	}
	if p.BaseURL == "" {
		res.Err = fmt.Errorf("provider %q has no base URL", p.Name)
		return res
	}
	if caps == nil {
		res.Err = fmt.Errorf("capability registry is not available")
		return res
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	var ids []string
	var err error
	apiKey := llm.KiloDefaultKey(p.BaseURL, p.APIKey)
	if p.Type == config.ProviderAnthropic {
		ids, err = llm.ListAnthropicModels(ctx, p.BaseURL, apiKey)
	} else if freeOnlyProvider(p) {
		// Public OpenCode/Kilo catalogs contain paid entries as well. Their
		// metadata (Kilo isFree / zero pricing, OpenCode's explicit free IDs) is
		// the authority; downloading every ID and guessing later leaked hundreds
		// of unusable models into the picker.
		ids, err = llm.ListFreeModels(ctx, p.BaseURL, apiKey)
	} else {
		ids, err = llm.ListProviderModels(ctx, p.BaseURL, apiKey)
	}
	cancel()
	if err != nil {
		res.Err = err
		return res
	}
	for _, id := range ids {
		mi := llm.HeuristicCapabilities(id)
		mi.Provider = p.Name
		caps.Register(mi)
	}
	res.Models = ids
	return res
}
