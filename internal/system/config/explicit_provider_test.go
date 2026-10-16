package config

import (
	"os"
	"testing"
)

// An explicit --provider codex must not inherit a default LM Studio URL.
func TestTomlConfigToEnvForProviderSkipsOtherType(t *testing.T) {
	for _, k := range []string{"SUPERCLI_LLM_MODEL", "SUPERCLI_LLM_PROVIDER", "SUPERCLI_LLM_BASE_URL", "SUPERCLI_LLM_API_KEY", "OPENAI_API_KEY"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	toml := TomlConfig{
		DefaultModel:    "local-model",
		DefaultProvider: "lmstudio",
		Providers: []ProviderConf{
			{Name: "lmstudio", Type: ProviderOpenAI, BaseURL: "http://localhost:1234/v1"},
			{Name: "codex", Type: ProviderCodex, BaseURL: "https://chatgpt.com/backend-api/codex"},
		},
	}
	TomlConfigToEnvForProvider(toml, ProviderCodex)
	if v := os.Getenv("SUPERCLI_LLM_BASE_URL"); v != "https://chatgpt.com/backend-api/codex" {
		t.Fatalf("base URL = %q", v)
	}
	if v := os.Getenv("SUPERCLI_LLM_MODEL"); v != "" {
		t.Fatalf("default model of another provider leaked: %q", v)
	}
}

func TestLoadDefaultBaseURLFollowsProvider(t *testing.T) {
	t.Setenv("SUPERCLI_LLM_BASE_URL", "")
	os.Unsetenv("SUPERCLI_LLM_BASE_URL")
	c, err := Load(FlagOverrides{Provider: ProviderCodex})
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseURL != "https://chatgpt.com/backend-api/codex" {
		t.Fatalf("codex base URL = %q", c.BaseURL)
	}
	c, err = Load(FlagOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("default base URL = %q", c.BaseURL)
	}
}
