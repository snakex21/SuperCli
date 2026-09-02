package factory

import (
	"fmt"
	"strings"
	"time"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

// BuildChain builds the selected provider plus an explicitly configured,
// ordered fallback_models chain. With an empty list it is exactly Build and
// creates no wrapper or additional providers.
func (f *Factory) BuildChain(cfg config.Config, tc config.TomlConfig, purpose string) (llm.Provider, error) {
	primary, err := f.Build(cfg, purpose)
	if err != nil {
		return primary, err
	}
	fallbackModels := append([]string(nil), tc.FallbackModels...)
	cooldown := time.Duration(tc.FallbackCooldownSeconds) * time.Second
	if len(fallbackModels) == 0 {
		return primary, nil
	}
	pool := []llm.Provider{primary}
	labels := []string{cfg.Provider + "/" + cfg.Model}
	seen := map[string]bool{backendKey(cfg): true}
	for _, reference := range fallbackModels {
		providerList := tc.Providers
		if name, _, found := strings.Cut(strings.TrimSpace(reference), "/"); found {
			if resolved, ok := config.ResolveProviderConf(f.dataDir, tc, name); ok {
				has := false
				for _, candidate := range providerList {
					if candidate.Name == name {
						has = true
						break
					}
				}
				if !has {
					providerList = append(append([]config.ProviderConf(nil), providerList...), resolved)
				}
			}
		}
		fallbackCfg, err := config.ResolveModelReference(cfg, providerList, reference)
		if err != nil {
			return nil, fmt.Errorf("fallback_models %q: %w", reference, err)
		}
		key := backendKey(fallbackCfg)
		if seen[key] {
			continue
		}
		seen[key] = true
		provider, err := f.Build(fallbackCfg, purpose)
		if err != nil {
			return nil, fmt.Errorf("fallback_models %q: %w", reference, err)
		}
		pool = append(pool, provider)
		labels = append(labels, strings.TrimSpace(reference))
	}
	if len(pool) == 1 {
		return primary, nil
	}
	return llm.NewFailover(cooldown, labels, pool...)
}

func backendKey(cfg config.Config) string {
	return strings.ToLower(strings.TrimRight(cfg.BaseURL, "/") + "\x00" + cfg.Provider + "\x00" + cfg.Model)
}
