// Package providers manages the provider list stored in
// config.toml. It provides CRUD operations on providers,
// connectivity probing, model visibility toggling, and
// manual price editing.
package providers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

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
	var endpoint string
	if p.Type == config.ProviderAnthropic {
		endpoint = base + "/models"
	} else {
		endpoint = llm.ResolveOpenAIEndpoints(p.BaseURL).Models
	}
	client := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequest("GET", endpoint, nil)
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
	llm.ApplyOpenCodeZenHeaders(req, p.BaseURL)
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
