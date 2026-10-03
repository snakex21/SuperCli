package app

import (
	"testing"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

func TestAppAnyRouterMixedModelsUseTheirNativeAPI(t *testing.T) {
	for _, configured := range []string{config.ProviderOpenAI, config.ProviderAnthropic, config.ProviderResponses} {
		for _, tc := range []struct{ model, protocol string }{
			{"claude-opus-4-7", "anthropic"}, {"gpt-6-astra-cc-format", "anthropic"},
			{"gpt-6-astra", "responses"}, {"gpt-4o", "openai"}, {"gemini-2.5-pro", "openai"},
		} {
			p, err := buildProvider(config.Config{Provider: configured, BaseURL: "https://anyrouter.top", APIKey: "fixture-key", Model: tc.model}, "", llm.NewCapabilityRegistry())
			if err != nil {
				t.Fatal(err)
			}
			actual := ""
			switch p.(type) {
			case *llm.AnthropicProvider:
				actual = "anthropic"
			case *llm.ResponsesProvider:
				actual = "responses"
			case *llm.OpenAIProvider:
				actual = "openai"
			}
			if actual != tc.protocol || p.Name() != tc.model {
				t.Fatalf("configured=%s model=%s: protocol=%s want=%s", configured, tc.model, actual, tc.protocol)
			}
		}
	}
}
