package app

import (
	"testing"

	"supercli/internal/llm"
	"supercli/internal/llm/factory"
	"supercli/internal/system/config"
)

// The CLI/TUI builder must route like factory.Default: official OpenAI
// reasoning models on api.openai.com go to Responses, everything else to chat.
func TestAppOpenAIReasoningModelsUseResponses(t *testing.T) {
	t.Setenv(factory.OpenAIReasoningResponsesEnv, "")
	for _, tc := range []struct {
		base, model string
		responses   bool
	}{
		{"https://api.openai.com/v1", "gpt-5.4", true},
		{"https://api.openai.com/v1", "o3", true},
		{"https://api.openai.com/v1", "gpt-4o", false},
		{"https://openrouter.ai/api/v1", "openai/gpt-5.4", false},
		{"http://127.0.0.1:8080/v1", "gpt-5.4", false},
	} {
		p, err := buildProvider(config.Config{Provider: config.ProviderOpenAI, BaseURL: tc.base, APIKey: "fixture-key", Model: tc.model}, t.TempDir(), llm.NewCapabilityRegistry())
		if err != nil {
			t.Fatal(err)
		}
		_, isResponses := p.(*llm.ResponsesProvider)
		if isResponses != tc.responses {
			t.Fatalf("%s @ %s: got %T, want responses=%v", tc.model, tc.base, p, tc.responses)
		}
	}
	t.Setenv(factory.OpenAIReasoningResponsesEnv, "false")
	p, err := buildProvider(config.Config{Provider: config.ProviderOpenAI, BaseURL: "https://api.openai.com/v1", APIKey: "fixture-key", Model: "gpt-5.4"}, t.TempDir(), llm.NewCapabilityRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*llm.OpenAIProvider); !ok {
		t.Fatalf("switch off: got %T, want chat provider", p)
	}
}
