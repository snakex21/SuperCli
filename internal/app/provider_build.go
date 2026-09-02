package app

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"

	"supercli/internal/account/pricing"
	"supercli/internal/llm"
	"supercli/internal/llm/shuffler"
	"supercli/internal/system/config"
)

func buildProvider(cfg config.Config, dataDir string, caps *llm.CapabilityRegistry) (llm.Provider, error) {
	if cfg.IsEcho() {
		return llm.NewEcho(cfg.Model)
	}
	zenInfo, zenModel := llm.ResolveOpenCodeZenModelMetadata(dataDir, cfg.BaseURL, cfg.Model, caps)
	if cfg.Provider == config.ProviderResponses || (zenModel && zenInfo.Transport == llm.ModelTransportResponses) {
		return llm.NewResponses(llm.ResponsesConfig{
			BaseURL:        cfg.BaseURL,
			APIKey:         cfg.APIKey,
			Model:          cfg.Model,
			Timeout:        cfg.Timeout,
			ConnectTimeout: cfg.ConnectTimeout,
			Capabilities:   caps,
		})
	}
	if zenModel && zenInfo.Transport == llm.ModelTransportAnthropic {
		return llm.NewAnthropic(llm.AnthropicConfig{
			BaseURL:        cfg.BaseURL,
			APIKey:         cfg.APIKey,
			Model:          cfg.Model,
			MaxTokens:      cfg.MaxTokens,
			Timeout:        cfg.Timeout,
			ConnectTimeout: cfg.ConnectTimeout,
			Capabilities:   caps,
		})
	}
	if zenModel && zenInfo.Transport == llm.ModelTransportGoogle {
		return nil, fmt.Errorf("opencode Zen model %q requires Google transport, which this engine does not support yet", cfg.Model)
	}
	if cfg.Provider == config.ProviderOpencode {
		// F15: opencode headless gateway. The
		// provider wraps an OpenAI-compat client
		// pointed at the local gateway URL. Model
		// discovery happens after construction
		// (caller probes /v1/models and registers
		// in the capability registry).
		p, err := llm.NewOpencode(llm.OpencodeConfig{
			BaseURL:      cfg.BaseURL,
			APIKey:       cfg.APIKey,
			Model:        cfg.Model,
			MaxTokens:    cfg.MaxTokens,
			Capabilities: caps,
		})
		if err != nil {
			return nil, fmt.Errorf("buildProvider opencode: %w", err)
		}
		// Probe /v1/models and register discovered
		// models in the F16 capability pool. This
		// is best-effort; if the gateway is down
		// we still return the provider — the user
		// can still use an explicit --model.
		models, _ := p.ProbeModels(context.Background())
		if len(models) > 0 {
			log.Printf("F15: discovered %d model(s) from opencode gateway", len(models))
		}
		return p, nil
	}
	if cfg.Provider == config.ProviderCodex {
		// ChatGPT-subscription auth: requests route to the
		// ChatGPT backend Responses API with the OAuth bearer
		// token from <data dir>/auth.json instead of an API key.
		//
		// Multi-account: when more than one account is logged in
		// (auth.json + auth-<label>.json), build one CodexProvider
		// per account and wrap them in a round-robin RouterProvider
		// so calls spread across accounts with failover. A single
		// account returns a plain CodexProvider — zero overhead and
		// byte-for-byte the old behaviour.
		return buildCodexPool(cfg, dataDir, caps)
	}
	if cfg.Provider == config.ProviderAnthropic {
		return llm.NewAnthropic(llm.AnthropicConfig{
			BaseURL:        cfg.BaseURL,
			APIKey:         cfg.APIKey,
			Model:          cfg.Model,
			MaxTokens:      cfg.MaxTokens,
			Timeout:        cfg.Timeout,
			ConnectTimeout: cfg.ConnectTimeout,
			Capabilities:   caps,
		})
	}
	// Kilo: use IP shuffler client for proxy rotation.
	var httpClient *http.Client
	if strings.Contains(cfg.BaseURL, "api.kilo.ai") {
		httpClient = shuffler.Global.HTTPClient()
	}
	return llm.NewOpenAI(llm.OpenAIConfig{
		BaseURL:        cfg.BaseURL,
		APIKey:         llm.KiloDefaultKey(cfg.BaseURL, cfg.APIKey),
		Model:          cfg.Model,
		MaxTokens:      cfg.MaxTokens,
		Timeout:        cfg.Timeout,
		ConnectTimeout: cfg.ConnectTimeout,
		HTTPClient:     httpClient,
		Capabilities:   caps,
	})
}

func providerNameForModel(caps *llm.CapabilityRegistry, model string) string {
	if caps == nil || model == "" {
		return ""
	}
	if info, ok := caps.Get(model); ok {
		return info.Provider
	}
	// RouterProvider.Name() decorates pooled accounts as
	// "model (N accounts)". Strip that display suffix for a second lookup.
	if strings.HasSuffix(strings.ToLower(model), " accounts)") {
		if i := strings.LastIndex(model, " ("); i > 0 {
			if info, ok := caps.Get(model[:i]); ok {
				return info.Provider
			}
		}
	}
	return ""
}

func isSubscriptionRuntimeProvider(p llm.Provider) bool {
	if p == nil {
		return false
	}
	_, ok := llm.Unwrap(p).(interface {
		RateLimits() (llm.CodexRateLimits, bool)
	})
	return ok
}

func applyPricingMetadata(caps *llm.CapabilityRegistry, entries []pricing.PriceEntry) {
	if caps == nil || len(entries) == 0 {
		return
	}
	infos := make([]llm.ModelInfo, 0, len(entries))
	for _, e := range entries {
		infos = append(infos, llm.ModelInfo{
			ID:            e.ModelID,
			InputCost:     e.InputPer1M,
			OutputCost:    e.OutputPer1M,
			ContextLength: e.ContextLength,
			Source:        llm.SourceExternal,
			LastVerified:  e.FetchedAt,
		})
	}
	applyModelInfoMetadata(caps, infos)
}

func applyModelInfoMetadata(caps *llm.CapabilityRegistry, infos []llm.ModelInfo) {
	if caps == nil || len(infos) == 0 {
		return
	}
	shortCounts := make(map[string]int)
	for _, m := range infos {
		if slash := strings.IndexByte(m.ID, '/'); slash > 0 && slash < len(m.ID)-1 {
			shortCounts[strings.ToLower(m.ID[slash+1:])]++
		}
	}
	for _, m := range infos {
		if m.ID == "" {
			continue
		}
		applyOneModelInfoMetadata(caps, m)
		// OpenRouter IDs are often provider/model (e.g.
		// deepseek/deepseek-chat), while the direct provider scan returns the
		// short model id (deepseek-chat) with Provider=deepseek. Mirror metadata
		// onto that existing short row when it is clearly the same provider.
		// Routers may advertise only the short id under their own provider name;
		// accept that alias when the external catalog has exactly one canonical
		// provider/model entry for the short id.
		if slash := strings.IndexByte(m.ID, '/'); slash > 0 && slash < len(m.ID)-1 {
			provider, shortID := m.ID[:slash], m.ID[slash+1:]
			existing, ok := caps.Get(shortID)
			sameProvider := ok && strings.EqualFold(existing.Provider, provider)
			uniqueAlias := shortCounts[strings.ToLower(shortID)] == 1
			if ok && (sameProvider || uniqueAlias) {
				copy := m
				copy.ID = shortID
				copy.Provider = existing.Provider
				applyOneModelInfoMetadata(caps, copy)
			}
		}
	}
}

func applyOneModelInfoMetadata(caps *llm.CapabilityRegistry, m llm.ModelInfo) {
	if existing, ok := caps.Get(m.ID); ok {
		if m.InputCost > 0 {
			existing.InputCost = m.InputCost
		}
		if m.OutputCost > 0 {
			existing.OutputCost = m.OutputCost
		}
		if existing.ContextLength == 0 && m.ContextLength > 0 {
			existing.ContextLength = m.ContextLength
		}
		if existing.Provider == "" {
			existing.Provider = m.Provider
		}
		if m.LastVerified.After(existing.LastVerified) {
			existing.LastVerified = m.LastVerified
		}
		caps.Register(existing)
		return
	}
	caps.Register(m)
}
