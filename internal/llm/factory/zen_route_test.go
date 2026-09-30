package factory

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync/atomic"
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
	// Keep this test non-parallel: the catalog client uses DefaultTransport.
	// Never consult the changing external catalog in this routing fixture.
	previousTransport := http.DefaultTransport
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	var catalogDials atomic.Int32
	transport.DialContext = func(context.Context, string, string) (net.Conn, error) {
		catalogDials.Add(1)
		return nil, errors.New("offline catalog fixture")
	}
	http.DefaultTransport = transport
	t.Cleanup(func() {
		http.DefaultTransport = previousTransport
		transport.CloseIdleConnections()
	})
	base := "https://opencode.ai/zen/v1"
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "mimo-v2.6-flash-free", Transport: llm.ModelTransportOpenAICompatible, ReasoningKnown: true, Provider: "opencode", Source: llm.SourceExternal})
	caps.Register(llm.ModelInfo{ID: "muse-spark-1.3-contributor-free", Transport: llm.ModelTransportResponses, ReasoningKnown: true, Provider: "opencode", Source: llm.SourceExternal})
	caps.Register(llm.ModelInfo{ID: "minimax-m2.5-free", Transport: llm.ModelTransportAnthropic, ReasoningKnown: true, Provider: "opencode", Source: llm.SourceExternal})

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
	if got := catalogDials.Load(); got != 0 {
		t.Fatalf("complete model metadata unexpectedly fetched catalog %d times", got)
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
	if got := catalogDials.Load(); got != 1 {
		t.Fatalf("unknown model should try the offline catalog once, got %d", got)
	}
}
