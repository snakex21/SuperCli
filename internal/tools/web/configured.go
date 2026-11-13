package web

import (
	"net/url"
	"os"
	"strings"

	"supercli/internal/system/config"
)

// NewConfiguredWebSearch builds web_search/web_lookup from [web_search]:
// the local engine (keyless DuckDuckGo by default) plus, when explicitly
// enabled, a provider-hosted search that falls back to it.
func NewConfiguredWebSearch(cfg config.TomlConfig) *WebSearch {
	ws := cfg.WebSearch
	key := ws.APIKey
	if key == "" {
		switch strings.ToLower(ws.Engine) {
		case "brave":
			key = os.Getenv("BRAVE_API_KEY")
		case "tavily":
			key = os.Getenv("TAVILY_API_KEY")
		case "exa":
			key = os.Getenv("EXA_API_KEY")
		}
	}
	searcher := NewWebSearch(ws.Engine, key, ws.BaseURL)
	if hosted, ok := hostedSearchConfig(cfg, os.Getenv); ok {
		searcher.WithHosted(hosted)
	}
	return searcher
}

// hostedSearchConfig resolves [web_search] hosted. It is off unless the
// user chose "openai", "xai" or "auto": hosted calls are billed per search
// and send the query to the provider, while the local engines need nothing.
func hostedSearchConfig(cfg config.TomlConfig, getenv func(string) string) (HostedSearchConfig, bool) {
	ws := cfg.WebSearch
	mode := strings.ToLower(strings.TrimSpace(ws.Hosted))
	var candidates []string
	switch mode {
	case "openai", "xai":
		candidates = []string{mode}
	case "auto":
		candidates = []string{"openai", "xai"}
	default:
		return HostedSearchConfig{}, false
	}
	for _, provider := range candidates {
		key := ws.HostedAPIKey
		if key == "" {
			key = hostedProviderKey(cfg, provider, getenv)
		}
		if key != "" {
			return HostedSearchConfig{Provider: provider, Model: ws.HostedModel, APIKey: key, BaseURL: ws.HostedBaseURL}, true
		}
	}
	return HostedSearchConfig{}, false
}

// hostedProviderKey finds a key for the provider's own API: its standard
// environment variable, then a configured [[providers]] entry pointing at
// that API host.
func hostedProviderKey(cfg config.TomlConfig, provider string, getenv func(string) string) string {
	envName, host := "OPENAI_API_KEY", "api.openai.com"
	if provider == "xai" {
		envName, host = "XAI_API_KEY", "api.x.ai"
	}
	if key := strings.TrimSpace(getenv(envName)); key != "" {
		return key
	}
	for _, p := range cfg.Providers {
		if p.Disabled || p.APIKey == "" {
			continue
		}
		if u, err := url.Parse(p.BaseURL); err == nil && strings.EqualFold(u.Hostname(), host) {
			return p.APIKey
		}
	}
	return ""
}
