// Package providers manages the provider list stored in
// config.toml. It provides CRUD operations on providers,
// connectivity probing, model visibility toggling, and
// manual price editing.
package providers

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

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
	var discovered []llm.ModelInfo
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
		if err == nil {
			if all, listErr := llm.ListProviderModelInfos(ctx, p.BaseURL, apiKey); listErr == nil {
				allowed := make(map[string]struct{}, len(ids))
				for _, id := range ids {
					allowed[id] = struct{}{}
				}
				for _, model := range all {
					if _, ok := allowed[model.ID]; ok {
						discovered = append(discovered, model)
					}
				}
			}
		}
	} else {
		discovered, err = llm.ListProviderModelInfos(ctx, p.BaseURL, apiKey)
		for _, model := range discovered {
			ids = append(ids, model.ID)
		}
	}
	// Native local endpoints are consulted only for context-window sizes. Their
	// vision/tool flags are deliberately ignored: those hints must not become a
	// second permission layer in front of the selected model.
	if err == nil && p.Type != config.ProviderAnthropic {
		discovered = mergeDiscoveredContextLengths(discovered, llm.ListLocalNativeModelInfos(ctx, p.BaseURL, apiKey))
	}
	cancel()
	if err != nil {
		res.Err = err
		return res
	}
	byID := make(map[string]llm.ModelInfo, len(discovered))
	for _, model := range discovered {
		byID[model.ID] = model
	}
	for _, id := range ids {
		mi, ok := byID[id]
		if !ok {
			mi = llm.HeuristicCapabilities(id)
		}
		mi.Provider = p.Name
		caps.Register(mi)
	}
	res.Models = ids
	return res
}

func mergeDiscoveredContextLengths(base, native []llm.ModelInfo) []llm.ModelInfo {
	byID := make(map[string]int, len(base))
	for index, model := range base {
		byID[model.ID] = index
	}
	for _, model := range native {
		if model.ContextLength <= 0 {
			continue
		}
		if index, ok := byID[model.ID]; ok {
			base[index].ContextLength = model.ContextLength
		}
	}
	return base
}
