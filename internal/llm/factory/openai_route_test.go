package factory

import (
	"os"
	"path/filepath"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

func buildRaw(t *testing.T, dataDir string, cfg config.Config) llm.Provider {
	t.Helper()
	p, err := New(nil, dataDir, llm.NewCapabilityRegistry()).Build(cfg, llm.PurposeMain)
	if err != nil {
		t.Fatalf("Build(%s @ %s): %v", cfg.Model, cfg.BaseURL, err)
	}
	return llm.Unwrap(p)
}

func TestOpenAIReasoningModelsOnOfficialAPIUseResponses(t *testing.T) {
	t.Setenv(OpenAIReasoningResponsesEnv, "")
	dir := t.TempDir()
	for _, model := range []string{"gpt-5.4", "gpt-5.4-nano", "o3", "o4-mini", "codex-mini-latest"} {
		for _, base := range []string{"https://api.openai.com/v1", "https://api.openai.com/v1/", ""} {
			cfg := config.Config{Provider: config.ProviderOpenAI, Model: model, BaseURL: base, APIKey: "sk-test"}
			if _, ok := buildRaw(t, dir, cfg).(*llm.ResponsesProvider); !ok {
				t.Fatalf("%s @ %q: want *ResponsesProvider, got %T", model, base, buildRaw(t, dir, cfg))
			}
		}
	}
}

func TestOpenAIChatPathKeptOutsideOfficialReasoningModels(t *testing.T) {
	t.Setenv(OpenAIReasoningResponsesEnv, "")
	dir := t.TempDir()
	cases := []config.Config{
		{Provider: config.ProviderOpenAI, Model: "gpt-4o-mini", BaseURL: "https://api.openai.com/v1", APIKey: "k"},
		{Provider: config.ProviderOpenAI, Model: "gpt-4.1", BaseURL: "https://api.openai.com/v1", APIKey: "k"},
		{Provider: config.ProviderOpenAI, Model: "gpt-5.4", BaseURL: "http://127.0.0.1:8080/v1", APIKey: "k"},
		{Provider: config.ProviderOpenAI, Model: "openai/gpt-5.4", BaseURL: "https://openrouter.ai/api/v1", APIKey: "k"},
		{Provider: config.ProviderOpenAI, Model: "gpt-5.4", BaseURL: "https://example.openai.azure.com/openai/v1", APIKey: "k"},
		{Provider: config.ProviderOpenAI, Model: "gpt-5.4", BaseURL: "https://api.openai.com.evil.test/v1", APIKey: "k"},
	}
	for _, cfg := range cases {
		if _, ok := buildRaw(t, dir, cfg).(*llm.OpenAIProvider); !ok {
			t.Fatalf("%s @ %s: want *OpenAIProvider, got %T", cfg.Model, cfg.BaseURL, buildRaw(t, dir, cfg))
		}
	}
}

func TestOpenAIReasoningResponsesSwitch(t *testing.T) {
	cfg := config.Config{Provider: config.ProviderOpenAI, Model: "gpt-5.4", BaseURL: "https://api.openai.com/v1", APIKey: "k"}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("openai_reasoning_responses = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(OpenAIReasoningResponsesEnv, "")
	if _, ok := buildRaw(t, dir, cfg).(*llm.OpenAIProvider); !ok {
		t.Fatalf("config switch off: want *OpenAIProvider, got %T", buildRaw(t, dir, cfg))
	}
	t.Setenv(OpenAIReasoningResponsesEnv, "on")
	if _, ok := buildRaw(t, dir, cfg).(*llm.ResponsesProvider); !ok {
		t.Fatalf("env on overrides config: want *ResponsesProvider, got %T", buildRaw(t, dir, cfg))
	}
	t.Setenv(OpenAIReasoningResponsesEnv, "0")
	if UseResponsesForOpenAI(cfg, t.TempDir()) {
		t.Fatal("env 0 must disable routing")
	}
}

func TestOpenAIResponsesBaseURL(t *testing.T) {
	for in, want := range map[string]string{
		"":                           "https://api.openai.com/v1",
		"https://api.openai.com/v1/": "https://api.openai.com/v1",
		"https://api.openai.com/v1/chat/completions": "https://api.openai.com/v1",
	} {
		if got := OpenAIResponsesBaseURL(in); got != want {
			t.Fatalf("OpenAIResponsesBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}
