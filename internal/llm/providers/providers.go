// Package providers manages the provider list stored in
// config.toml. It provides CRUD operations on providers,
// connectivity probing, model visibility toggling, and
// manual price editing.
package providers

import (
	"context"
	"fmt"
	"net/http"
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
			Name:    p.Name,
			Type:    p.Type,
			BaseURL: p.BaseURL,
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
			Name:    p.Name,
			Type:    p.Type,
			BaseURL: p.BaseURL,
			Model:   p.Model,
			HasKey:  strings.TrimSpace(p.APIKey) != "",
		}
		if caps != nil {
			for _, mi := range caps.All() {
				if mi.Provider == p.Name && isDiscoveredProviderModel(mi) && modelVisibleForProvider(p, mi.ID) {
					pi.Models = append(pi.Models, mi)
				}
			}
		}
		out = append(out, pi)
	}
	return out
}

// Add appends a new provider to the config.toml list.
func (m *Manager) Add(name, typ, baseURL, apiKey, model string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

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
		APIKey:  llm.KiloDefaultKey(baseURL, llm.CleanAPIKey(apiKey)),
		Model:   model,
	}
	m.providers = append(m.providers, p)
	return m.saveLocked()
}

// Remove removes a provider by name from the config.toml list.
func (m *Manager) Remove(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	found := false
	var filtered []config.ProviderConf
	for _, p := range m.providers {
		if p.Name == name {
			found = true
			continue
		}
		filtered = append(filtered, p)
	}
	if !found {
		return fmt.Errorf("provider %q not found", name)
	}
	m.providers = filtered
	return m.saveLocked()
}

// Update modifies an existing provider's fields. Only non-empty
// values are applied.
func (m *Manager) Update(name string, typ, baseURL, apiKey, model *string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, p := range m.providers {
		if p.Name == name {
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
			return m.saveLocked()
		}
	}
	return fmt.Errorf("provider %q not found", name)
}

// IsHidden returns true if a model ID has visibility toggled off.
func (m *Manager) IsHidden(modelID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.hidden[modelID]
	return ok
}

// ToggleHidden flips a model's visibility. Returns the new state
// (true = hidden).
func (m *Manager) ToggleHidden(modelID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.hidden[modelID]; ok {
		delete(m.hidden, modelID)
		return false
	}
	m.hidden[modelID] = struct{}{}
	return true
}

// ShowModel ensures a model is visible (removes it from the
// hidden set if present). Returns true if the model was
// previously hidden and is now visible.
func (m *Manager) ShowModel(modelID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.hidden[modelID]; ok {
		delete(m.hidden, modelID)
		m.saveHiddenLocked()
		return true
	}
	return false
}

// HideModel hides a model (adds it to the hidden set).
// Returns true if the model was previously visible.
func (m *Manager) HideModel(modelID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.hidden[modelID]; !ok {
		m.hidden[modelID] = struct{}{}
		m.saveHiddenLocked()
		return true
	}
	return false
}

// LoadHiddenState loads the list of hidden model IDs from
// config.toml into the in-memory hidden map.
func (m *Manager) LoadHiddenState() {
	m.mu.Lock()
	defer m.mu.Unlock()

	tc, err := config.LoadToml(m.tomlPath)
	if err != nil {
		return
	}
	for _, id := range tc.HiddenModels {
		if id != "" {
			m.hidden[id] = struct{}{}
		}
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
	for id := range m.hidden {
		ids = append(ids, id)
	}
	tc.HiddenModels = ids
	config.SaveToml(m.tomlPath, tc)
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
		if _, hidden := m.hidden[mi.ID]; hidden {
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

func (m *Manager) modelVisibleLocked(provider, id string) bool {
	if p, ok := m.providerByNameLocked(provider); ok {
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
	m.mu.Lock()
	defer m.mu.Unlock()

	// Update in-memory TOML config.
	tc, err := config.LoadToml(m.tomlPath)
	if err != nil {
		// No config.toml yet — create one.
		tc = config.TomlConfig{}
	}

	// Find existing entry or append.
	found := false
	for i, mp := range tc.ModelPrices {
		if mp.Model == modelID {
			tc.ModelPrices[i].InputCost = inputCost
			tc.ModelPrices[i].OutputCost = outputCost
			found = true
			break
		}
	}
	if !found {
		tc.ModelPrices = append(tc.ModelPrices, config.ModelPriceConf{
			Model:      modelID,
			InputCost:  inputCost,
			OutputCost: outputCost,
		})
	}

	return config.SaveToml(m.tomlPath, tc)
}

// SetModelPrices pushes user-defined prices into the capability
// registry and credits system. Called on startup and after price edits.
func (m *Manager) SetModelPrices(caps *llm.CapabilityRegistry) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tc, err := config.LoadToml(m.tomlPath)
	if err != nil {
		return
	}
	for _, mp := range tc.ModelPrices {
		if existing, ok := caps.Get(mp.Model); ok {
			existing.InputCost = mp.InputCost
			existing.OutputCost = mp.OutputCost
			caps.Register(existing) // SourceUser priority
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
		res := scanProviderConf(p, caps)
		if res.Err == nil {
			total += len(res.Models)
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
	return scanProviderConf(found, caps)
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
	} else {
		ids, err = llm.ListProviderModels(ctx, p.BaseURL, apiKey)
	}
	cancel()
	if err != nil {
		res.Err = err
		return res
	}
	// Kilo/OpenCode public access = free models only; own key = all models.
	if freeOnlyProvider(p) {
		var free []string
		for _, id := range ids {
			if llm.IsFreeModelID(id) {
				free = append(free, id)
			}
		}
		ids = free
	}
	for _, id := range ids {
		mi := llm.HeuristicCapabilities(id)
		mi.Provider = p.Name
		caps.Register(mi)
	}
	res.Models = ids
	return res
}
