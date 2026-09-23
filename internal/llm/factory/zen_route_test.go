package factory

import (
	"testing"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

// TestZenOpenAICompatRouting pins the catalog-driven Zen transport rule:
// free-tier routing comes from models.dev (Transport on ModelInfo), never
// from cfg.Provider. openai-compatible models must land on *OpenAIProvider
// (chat/completions) even when the user config forces provider=responses;
// muse (responses) must stay on *ResponsesProvider. Unknown Zen models
// default to OpenAI chat so a newly published free model works as soon as
// it appears on /models — until the catalog describes it, and forever
// after via Transport, without a SuperCli code change.
func TestZenOpenAICompatRouting(t *testing.T) {
	base := "https://opencode.ai/zen/v1"
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "mimo-v2.6-flash-free", Transport: llm.ModelTransportOpenAICompatible, Provider: "opencode", Source: llm.SourceExternal})
	caps.Register(llm.ModelInfo{ID: "muse-spark-1.3-contributor-free", Transport: llm.ModelTransportResponses, Provider: "opencode", Source: llm.SourceExternal})
	caps.Register(llm.ModelInfo{ID: "minimax-m2.5-free", Transport: llm.ModelTransportAnthropic, Provider: "opencode", Source: llm.SourceExternal})

	for _, prov := range []string{config.ProviderResponses, config.ProviderOpenAI} {
		p, err := Default(config.Config{Provider: prov, BaseURL: base, Model: "mimo-v2.6-flash-free"}, t.TempDir(), caps)
		if err != nil {
			t.Fatalf("mimo prov=%s err=%v", prov, err)
		}
		if _, ok := llm.Unwrap(p).(*llm.OpenAIProvider); !ok {
			t.Fatalf("mimo prov=%s type=%T, want *llm.OpenAIProvider", prov, llm.Unwrap(p))
		}

		p2, err := Default(config.Config{Provider: prov, BaseURL: base, Model: "muse-spark-1.3-contributor-free"}, t.TempDir(), caps)
		if err != nil {
			t.Fatalf("muse prov=%s err=%v", prov, err)
		}
		if _, ok := llm.Unwrap(p2).(*llm.ResponsesProvider); !ok {
			t.Fatalf("muse prov=%s type=%T, want *llm.ResponsesProvider", prov, llm.Unwrap(p2))
		}

		p3, err := Default(config.Config{Provider: prov, BaseURL: base, Model: "minimax-m2.5-free"}, t.TempDir(), caps)
		if err != nil {
			t.Fatalf("minimax prov=%s err=%v", prov, err)
		}
		if _, ok := llm.Unwrap(p3).(*llm.AnthropicProvider); !ok {
			t.Fatalf("minimax prov=%s type=%T, want *llm.AnthropicProvider", prov, llm.Unwrap(p3))
		}
	}

	// Unknown free-tier model on a Zen base URL (catalog miss / brand-new
	// release): default to OpenAI chat so the gate tools apply — not the
	// user's provider=responses force.
	p, err := Default(config.Config{Provider: config.ProviderResponses, BaseURL: base, Model: "brand-new-free"}, t.TempDir(), caps)
	if err != nil {
		t.Fatalf("unknown zen model: %v", err)
	}
	if _, ok := llm.Unwrap(p).(*llm.OpenAIProvider); !ok {
		t.Fatalf("unknown zen model type=%T, want *llm.OpenAIProvider", llm.Unwrap(p))
	}
}
