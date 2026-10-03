package llm

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAnyRouterModelProtocolAndOrigin(t *testing.T) {
	for _, base := range []string{"https://anyrouter.top", " https://ANYROUTER.top:443/v1/ ", "https://anyrouter.top/v1/models", "https://anyrouter.top/v1/messages", "https://anyrouter.top/v1/responses", "https://anyrouter.top/v1/chat/completions"} {
		for model, want := range map[string]string{
			"claude-opus-4-7": "anthropic", "gpt-6-astra-cc-format": "anthropic",
			"gpt-6-astra": "responses", "gpt-5-codex": "responses", "o3-mini": "responses",
			"gpt-4o": "openai", "gpt-3.5-turbo": "openai", "gemini-2.5-pro": "openai",
			"custom-model": "", "no model": "",
		} {
			if got := AnyRouterModelProtocol(base, model); got != want {
				t.Fatalf("%s/%s: protocol=%q want=%q", base, model, got, want)
			}
		}
		if got := NormalizeAnyRouterBaseURL(base); !strings.HasSuffix(got, "/v1") {
			t.Fatalf("endpoint root not normalized: %q", got)
		}
	}
	for _, base := range []string{"https://anyrouter.top.evil.invalid/v1", "https://anyrouter.top@evil.invalid/v1", "https://other.invalid/anyrouter.top", "http://anyrouter.top/v1", "https://anyrouter.top:8443/v1", "https://anyrouter.top/proxy/v1", "https://anyrouter.top/v1?other=1", "https://opencode.ai/zen/v1", "https://api.openai.com/v1", "https://api.anthropic.com/v1"} {
		if IsAnyRouterBaseURL(base) || AnyRouterModelProtocol(base, "gpt-6-astra") != "" || NormalizeAnyRouterBaseURL(base) != base {
			t.Fatalf("unrelated origin changed: %q", base)
		}
	}
}

type anyRouterTestTransport func(*http.Request) (*http.Response, error)

func (f anyRouterTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAnyRouterMessagesRootAndBetaBeforeFirstRequest(t *testing.T) {
	clearEndpointBetas()
	t.Cleanup(clearEndpointBetas)
	requests := 0
	client := &http.Client{Transport: anyRouterTestTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.URL.String() != "https://anyrouter.top/v1/messages" || r.Header.Get("anthropic-beta") != anthropicBetaContext1M || r.Header.Get("x-api-key") != "fixture-key" {
			t.Fatalf("incorrect first Messages request: path=%s beta=%q", r.URL, r.Header.Get("anthropic-beta"))
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Request: r, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"OK\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"))}, nil
	})}
	p, err := NewAnthropic(AnthropicConfig{BaseURL: "https://anyrouter.top", APIKey: "fixture-key", Model: "claude-fixture", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "Reply OK"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var text string
	for delta := range stream {
		if delta.Err != nil {
			t.Fatal(delta.Err)
		}
		text += delta.Content
	}
	if text != "OK" || requests != 1 {
		t.Fatalf("reply=%q requests=%d; beta must not cost a retry", text, requests)
	}
}

func TestAnyRouterPassiveDetectionDoesNotProbe(t *testing.T) {
	typ, err := DetectProviderProtocol(context.Background(), "https://anyrouter.top", "fixture-key")
	if err != nil || typ != "openai" {
		t.Fatalf("catalog protocol=%q err=%v", typ, err)
	}
}
