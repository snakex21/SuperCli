package app

// Tests for resolveTaskWorkerConfig — the config `task_model` →
// worker-provider mapping for model-per-task delegation. The contract:
// unset (or a no-op value) returns ok=false so workers inherit the
// coordinator's provider with zero behaviour change.

import (
	"testing"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

func taskModelBaseCfg() config.Config {
	return config.Config{
		Provider: "openai",
		BaseURL:  "http://127.0.0.1:8089/v1",
		APIKey:   "main-key",
		Model:    "qwen3.5-9b",
	}
}

// TestResolveTaskWorkerConfig_UnsetIsNoop: no task_model → ok=false and
// the returned config is the coordinator's, untouched.
func TestResolveTaskWorkerConfig_UnsetIsNoop(t *testing.T) {
	cfg := taskModelBaseCfg()
	got, ok := resolveTaskWorkerConfig(config.TomlConfig{}, cfg)
	if ok {
		t.Fatal("unset task_model must not report an override")
	}
	if got != cfg {
		t.Fatalf("config changed without an override: %+v", got)
	}
}

// TestResolveTaskWorkerConfig_BareModelSwapsModelOnly: "model-id" keeps
// the coordinator's transport (base URL, key, type) and swaps the model,
// draft_model style.
func TestResolveTaskWorkerConfig_BareModelSwapsModelOnly(t *testing.T) {
	cfg := taskModelBaseCfg()
	got, ok := resolveTaskWorkerConfig(config.TomlConfig{TaskModel: "ministral-3b"}, cfg)
	if !ok {
		t.Fatal("expected an override")
	}
	if got.Model != "ministral-3b" {
		t.Errorf("Model = %q", got.Model)
	}
	if got.BaseURL != cfg.BaseURL || got.APIKey != cfg.APIKey || got.Provider != cfg.Provider {
		t.Errorf("bare form must keep the coordinator transport: %+v", got)
	}
}

// TestResolveTaskWorkerConfig_ProviderFormSwitchesHost: a
// "providerName/model" label resolves the [[providers]] entry so the
// worker hits a different host with its own key — and the worker's
// base URL is what host-gated behaviour (cache_prompt auto) must see.
func TestResolveTaskWorkerConfig_ProviderFormSwitchesHost(t *testing.T) {
	cfg := taskModelBaseCfg()
	tomlCfg := config.TomlConfig{
		TaskModel: "small/ministral-3b",
		Providers: []config.ProviderConf{
			{Name: "small", Type: "openai", BaseURL: "http://127.0.0.1:8091/v1", APIKey: "small-key"},
		},
	}
	got, ok := resolveTaskWorkerConfig(tomlCfg, cfg)
	if !ok {
		t.Fatal("expected an override")
	}
	if got.BaseURL != "http://127.0.0.1:8091/v1" || got.APIKey != "small-key" || got.Model != "ministral-3b" {
		t.Errorf("provider form not resolved: %+v", got)
	}
	// The worker provider is built from THIS config, so llama.cpp
	// request hints (cache_prompt) are gated on the worker's host.
	if !llm.IsLocalBaseURL(got.BaseURL) {
		t.Error("worker base URL should be recognized as local")
	}
}

// TestResolveTaskWorkerConfig_ProviderFormDefaultsModel: an empty model
// after the slash falls back to the provider entry's configured model.
func TestResolveTaskWorkerConfig_ProviderFormDefaultsModel(t *testing.T) {
	cfg := taskModelBaseCfg()
	tomlCfg := config.TomlConfig{
		TaskModel: "small/",
		Providers: []config.ProviderConf{
			{Name: "small", Type: "openai", BaseURL: "http://127.0.0.1:8091/v1", Model: "ministral-3b"},
		},
	}
	got, ok := resolveTaskWorkerConfig(tomlCfg, cfg)
	if !ok || got.Model != "ministral-3b" {
		t.Fatalf("provider default model not used: ok=%v cfg=%+v", ok, got)
	}
}

// TestResolveTaskWorkerConfig_SlashModelIDWithoutProviderMatch: a first
// segment matching no configured provider is a bare model id
// (OpenRouter-style ids contain slashes) on the coordinator transport.
func TestResolveTaskWorkerConfig_SlashModelIDWithoutProviderMatch(t *testing.T) {
	cfg := taskModelBaseCfg()
	got, ok := resolveTaskWorkerConfig(config.TomlConfig{TaskModel: "qwen/qwen-2.5-7b"}, cfg)
	if !ok {
		t.Fatal("expected an override")
	}
	if got.Model != "qwen/qwen-2.5-7b" || got.BaseURL != cfg.BaseURL {
		t.Errorf("slash id not treated as bare model: %+v", got)
	}
}

// TestResolveTaskWorkerConfig_SameBackendIsNoop: naming the exact model
// the coordinator already runs (bare or provider form pointing at the
// same host+model) reports no override — default path, zero change.
func TestResolveTaskWorkerConfig_SameBackendIsNoop(t *testing.T) {
	cfg := taskModelBaseCfg()
	if _, ok := resolveTaskWorkerConfig(config.TomlConfig{TaskModel: "qwen3.5-9b"}, cfg); ok {
		t.Error("bare form naming the coordinator model must be a no-op")
	}
	tomlCfg := config.TomlConfig{
		TaskModel: "main/qwen3.5-9b",
		Providers: []config.ProviderConf{
			{Name: "main", Type: "openai", BaseURL: cfg.BaseURL, APIKey: cfg.APIKey},
		},
	}
	if _, ok := resolveTaskWorkerConfig(tomlCfg, cfg); ok {
		t.Error("provider form naming the coordinator backend must be a no-op")
	}
}

// TestResolveTaskWorkerConfig_ProviderWithoutModelIsNoop: a provider
// entry with no model at all cannot be used — fall back, don't error.
func TestResolveTaskWorkerConfig_ProviderWithoutModelIsNoop(t *testing.T) {
	cfg := taskModelBaseCfg()
	tomlCfg := config.TomlConfig{
		TaskModel: "small/",
		Providers: []config.ProviderConf{
			{Name: "small", Type: "openai", BaseURL: "http://127.0.0.1:8091/v1"},
		},
	}
	got, ok := resolveTaskWorkerConfig(tomlCfg, cfg)
	if ok {
		t.Fatal("provider with no model must not report an override")
	}
	if got != cfg {
		t.Fatalf("config changed without an override: %+v", got)
	}
}
