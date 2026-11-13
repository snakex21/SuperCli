package web

import (
	"testing"

	"supercli/internal/system/config"
)

func TestHostedSearchConfigIsOffByDefaultAndResolvesKeys(t *testing.T) {
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }
	cfg := config.TomlConfig{Providers: []config.ProviderConf{
		{Name: "openai", Type: "openai", BaseURL: "https://api.openai.com/v1", APIKey: "from-provider"},
		{Name: "local", Type: "openai", BaseURL: "http://127.0.0.1:1234/v1", APIKey: "lm-studio"},
	}}
	if _, ok := hostedSearchConfig(cfg, getenv); ok {
		t.Fatal("hosted search enabled without opt-in")
	}
	cfg.WebSearch.Hosted = "openai"
	if got, ok := hostedSearchConfig(cfg, getenv); !ok || got.Provider != "openai" || got.APIKey != "from-provider" {
		t.Fatalf("provider key not reused: %+v %v", got, ok)
	}
	env["OPENAI_API_KEY"] = "from-env"
	if got, _ := hostedSearchConfig(cfg, getenv); got.APIKey != "from-env" {
		t.Fatalf("env key not preferred: %+v", got)
	}
	cfg.WebSearch.HostedAPIKey = "explicit"
	cfg.WebSearch.HostedModel = "gpt-5.4-mini"
	if got, _ := hostedSearchConfig(cfg, getenv); got.APIKey != "explicit" || got.Model != "gpt-5.4-mini" {
		t.Fatalf("explicit settings ignored: %+v", got)
	}
	cfg.WebSearch = config.WebSearchConf{Hosted: "xai"}
	delete(env, "OPENAI_API_KEY")
	if _, ok := hostedSearchConfig(cfg, getenv); ok {
		t.Fatal("xai enabled without an xai key")
	}
	cfg.WebSearch.Hosted = "auto"
	env["XAI_API_KEY"] = "x"
	cfg.Providers = nil
	if got, ok := hostedSearchConfig(cfg, getenv); !ok || got.Provider != "xai" {
		t.Fatalf("auto did not pick the provider with a key: %+v %v", got, ok)
	}
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("XAI_API_KEY", "x")
	if engine := NewConfiguredWebSearch(cfg).HostedEngine(); engine != "xai-hosted" {
		t.Fatalf("tool not configured: %q", engine)
	}
}
