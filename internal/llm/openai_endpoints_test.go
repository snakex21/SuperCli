package llm

import "testing"

func TestResolveOpenAIEndpoints(t *testing.T) {
	tests := []struct {
		name, in, base, chat, models string
	}{
		{
			name: "bare domain", in: "https://reseller.example",
			base: "https://reseller.example/v1", chat: "https://reseller.example/v1/chat/completions", models: "https://reseller.example/v1/models",
		},
		{
			name: "version root", in: "https://reseller.example/v1/",
			base: "https://reseller.example/v1", chat: "https://reseller.example/v1/chat/completions", models: "https://reseller.example/v1/models",
		},
		{
			name: "full chat endpoint", in: "https://reseller.example/v1/chat/completions",
			base: "https://reseller.example/v1", chat: "https://reseller.example/v1/chat/completions", models: "https://reseller.example/v1/models",
		},
		{
			name: "full models endpoint", in: "https://reseller.example/v1/models",
			base: "https://reseller.example/v1", chat: "https://reseller.example/v1/chat/completions", models: "https://reseller.example/v1/models",
		},
		{
			name: "kilo exception", in: "https://api.kilo.ai/api/openrouter",
			base: "https://api.kilo.ai/api/openrouter", chat: "https://api.kilo.ai/api/openrouter/chat/completions", models: "https://api.kilo.ai/api/openrouter/models",
		},
		{
			name: "google explicit root", in: "https://generativelanguage.googleapis.com/v1beta/openai",
			base: "https://generativelanguage.googleapis.com/v1beta/openai", chat: "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions", models: "https://generativelanguage.googleapis.com/v1beta/openai/models",
		},
		{
			name: "custom api path is preserved", in: "https://reseller.example/api",
			base: "https://reseller.example/api", chat: "https://reseller.example/api/chat/completions", models: "https://reseller.example/api/models",
		},
		{
			name: "full endpoint with query", in: "https://reseller.example/v1/chat/completions?api-version=2026-01-01",
			base: "https://reseller.example/v1?api-version=2026-01-01", chat: "https://reseller.example/v1/chat/completions?api-version=2026-01-01", models: "https://reseller.example/v1/models?api-version=2026-01-01",
		},
		{
			name: "explicit custom chat endpoint", in: "https://reseller.example/custom/deployment/chat/completions",
			base: "https://reseller.example/custom/deployment", chat: "https://reseller.example/custom/deployment/chat/completions", models: "https://reseller.example/custom/deployment/models",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveOpenAIEndpoints(tt.in)
			if got.BaseURL != tt.base || got.ChatCompletions != tt.chat || got.Models != tt.models {
				t.Fatalf("ResolveOpenAIEndpoints(%q) = %+v, want base=%q chat=%q models=%q", tt.in, got, tt.base, tt.chat, tt.models)
			}
		})
	}
}
