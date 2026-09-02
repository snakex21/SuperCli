package llm

import (
	"testing"
	"time"
)

func TestParseOpenCodeZenCatalogUsesAdvertisedSDKAndCapabilities(t *testing.T) {
	data := []byte(`{
		"opencode": {
			"npm": "@ai-sdk/openai-compatible",
			"models": {
				"future-chat-free": {"reasoning":false,"tool_call":true,"modalities":{"input":["text"]}},
				"future-reasoner-free": {"provider":{"npm":"@ai-sdk/openai"},"reasoning":true,"tool_call":true,"attachment":true,"limit":{"context":999999}},
				"future-claude-free": {"provider":{"npm":"@ai-sdk/anthropic"},"reasoning":true,"tool_call":true},
				"future-gemini-free": {"provider":{"npm":"@ai-sdk/google"},"reasoning":true,"tool_call":true}
			}
		}
	}`)
	catalog, err := parseOpenCodeZenCatalog(data, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	wants := map[string]string{
		"future-chat-free":     ModelTransportOpenAICompatible,
		"future-reasoner-free": ModelTransportResponses,
		"future-claude-free":   ModelTransportAnthropic,
		"future-gemini-free":   ModelTransportGoogle,
	}
	for id, transport := range wants {
		info, ok := catalog.Models[id]
		if !ok || info.Transport != transport {
			t.Fatalf("%s metadata = %+v, want transport %q", id, info, transport)
		}
	}
	reasoner := catalog.Models["future-reasoner-free"]
	if !reasoner.Reasoning || !reasoner.ReasoningKnown || !reasoner.Vision || reasoner.ContextLength != 999999 {
		t.Fatalf("reasoner capabilities = %+v", reasoner)
	}
}

func TestResolveOpenCodeZenModelMetadataUsesPortableCacheWithoutModelAllowlist(t *testing.T) {
	dataDir := t.TempDir()
	catalog := openCodeZenCatalog{
		FetchedAt: time.Now().UTC(),
		Models: map[string]ModelInfo{
			"brand-new-free": {
				ID: "brand-new-free", Transport: ModelTransportResponses,
				Reasoning: true, ReasoningKnown: true, ToolUse: true,
			},
		},
	}
	if err := saveOpenCodeZenCatalog(dataDir, catalog); err != nil {
		t.Fatal(err)
	}
	caps := NewCapabilityRegistry()
	info, ok := ResolveOpenCodeZenModelMetadata(dataDir, "https://opencode.ai/zen/v1", "brand-new-free", caps)
	if !ok || info.Transport != ModelTransportResponses || !info.Reasoning {
		t.Fatalf("resolved metadata = %+v, ok=%v", info, ok)
	}
	registered, ok := caps.Get("brand-new-free")
	if !ok || registered.Transport != ModelTransportResponses || !registered.ReasoningKnown {
		t.Fatalf("registered metadata = %+v, ok=%v", registered, ok)
	}
}
