package llm

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// TestLooksLikeBetaRequired_Recognizes covers the refusals that must be read as
// "opt into this beta". The Chinese body is the one measured live against
// anyrouter on 2026-07-26; the rest are the same gate in other phrasings,
// because the recognition must not depend on the language the gateway speaks.
func TestLooksLikeBetaRequired_Recognizes(t *testing.T) {
	bodies := map[string]string{
		"chinese (live anyrouter body)": `{"error":"1m 上下文已经全量可用，请启用 1m 上下文后重试","type":"error"}`,
		"english":                       `{"error":{"message":"The 1M context is fully available, please enable the 1m context and retry"}}`,
		"names the feature id":          `{"error":{"message":"missing anthropic-beta: context-1m-2025-08-07"}}`,
		"1000k spelling":                `{"error":"1000k context must be enabled first"}`,
		"plain text body":               `1m context not enabled`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			header, ok := looksLikeBetaRequired(400, []byte(body))
			if !ok {
				t.Fatalf("looksLikeBetaRequired(400, %s) = not recognized, want the 1M context gate", body)
			}
			if header != anthropicBetaContext1M {
				t.Errorf("header = %q, want %q", header, anthropicBetaContext1M)
			}
		})
	}
}

// TestLooksLikeBetaRequired_Ignores is the regression half: every neighbouring
// failure that must NOT be answered with a header and a retry. A false positive
// here spends a request masking a real error.
func TestLooksLikeBetaRequired_Ignores(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"auth", 401, `{"error":{"message":"invalid x-api-key"}}`},
		{"forbidden", 403, `{"error":{"message":"forbidden"}}`},
		{"rate limited", 429, `{"error":{"message":"Service Unavailable","type":"error"}}`},
		{"upstream out of capacity", 503, `{"error":{"message":"Service Unavailable","type":"error"}}`},
		// The same gateway answers 429 for a model that has no beta gate; the
		// body must not be enough on its own to trigger a retry.
		{"gate wording on a non-400", 429, `{"error":"1m 上下文已经全量可用，请启用 1m 上下文后重试","type":"error"}`},
		{"context overflow", 400, `{"error":{"message":"prompt is too long: 1.2m tokens > 1m maximum"}}`},
		{"context_length_exceeded", 400, `{"error":{"code":"context_length_exceeded","message":"1m context window exceeded"}}`},
		{"model not served", 404, `{"error":"当前 API 不支持所选模型 gpt-5.6-sol","type":"error"}`},
		{"model rejected with a 1m-ish id", 400, `{"error":{"code":"model_not_found","message":"unknown model claude-4-1m context"}}`},
		{"unrelated validation error", 400, `{"error":{"message":"messages: roles must alternate"}}`},
		{"size marker without a context word", 400, `{"error":{"message":"the 1m plan is required"}}`},
		{"context word without a size marker", 400, `{"error":{"message":"context is required"}}`},
		{"1m inside an identifier", 400, `{"error":{"message":"request a1m3f9 failed while building context"}}`},
		{"empty body", 400, ``},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if header, ok := looksLikeBetaRequired(tt.status, []byte(tt.body)); ok {
				t.Fatalf("looksLikeBetaRequired(%d, %s) = %q, want no beta gate", tt.status, tt.body, header)
			}
		})
	}
}

// TestAnthropic_BetaGateSelfRepair is the end-to-end contract: a provider that
// answers the live anyrouter 400 must be reached anyway, on one retry, and the
// next call must go out correct the first time.
func TestAnthropic_BetaGateSelfRepair(t *testing.T) {
	t.Cleanup(clearEndpointBetas)
	clearEndpointBetas()

	var mu sync.Mutex
	var seen []string // anthropic-beta header per request, in order
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		beta := r.Header.Get("anthropic-beta")
		seen = append(seen, beta)
		mu.Unlock()
		if !strings.Contains(beta, anthropicBetaContext1M) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":"1m 上下文已经全量可用，请启用 1m 上下文后重试","type":"error"}`))
			return
		}
		sseResponse(w,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
			`{"type":"message_stop"}`,
		)
	})

	p, err := NewAnthropic(AnthropicConfig{BaseURL: srv.URL, APIKey: "key", Model: "claude-opus-4-8"})
	if err != nil {
		t.Fatal(err)
	}
	complete := func() string {
		t.Helper()
		ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		var body strings.Builder
		for _, d := range drainDeltas(t, ch) {
			if d.Err != nil {
				t.Fatalf("stream error: %v", d.Err)
			}
			body.WriteString(d.Content)
		}
		return body.String()
	}

	if got := complete(); got != "ok" {
		t.Fatalf("first call body = %q, want %q — the gate was not repaired", got, "ok")
	}
	if got := complete(); got != "ok" {
		t.Fatalf("second call body = %q, want %q", got, "ok")
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"", anthropicBetaContext1M, anthropicBetaContext1M}
	if fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Fatalf("anthropic-beta per request = %q, want %q (one retry, then remembered)", seen, want)
	}
}

// TestAnthropic_BetaGateRetriesOnlyOnce guards the failure mode a retry-on-error
// invites: an endpoint that keeps refusing must produce the error, not a loop.
func TestAnthropic_BetaGateRetriesOnlyOnce(t *testing.T) {
	t.Cleanup(clearEndpointBetas)
	clearEndpointBetas()

	var mu sync.Mutex
	requests := 0
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":"1m 上下文已经全量可用，请启用 1m 上下文后重试","type":"error"}`))
	})
	p, err := NewAnthropic(AnthropicConfig{BaseURL: srv.URL, APIKey: "key", Model: "claude-opus-4-8"})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var streamErr error
	for _, d := range drainDeltas(t, ch) {
		if d.Err != nil {
			streamErr = d.Err
		}
	}
	if streamErr == nil || !strings.Contains(streamErr.Error(), "http 400") {
		t.Fatalf("error = %v, want the 400 to surface", streamErr)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 2 {
		t.Fatalf("requests = %d, want 2 (original + exactly one retry)", requests)
	}
}

// TestAnthropic_NoBetaHeaderWithoutAGate: an endpoint that never asks must
// never be sent the header. This is the whole reason the repair is reactive.
func TestAnthropic_NoBetaHeaderWithoutAGate(t *testing.T) {
	t.Cleanup(clearEndpointBetas)
	clearEndpointBetas()

	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if beta := r.Header.Get("anthropic-beta"); beta != "" {
			t.Errorf("anthropic-beta = %q, want it unset on an endpoint that never asked", beta)
		}
		sseResponse(w,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
			`{"type":"message_stop"}`,
		)
	})
	p, err := NewAnthropic(AnthropicConfig{BaseURL: srv.URL, APIKey: "key", Model: "claude-opus-4-8"})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var body strings.Builder
	for _, d := range drainDeltas(t, ch) {
		body.WriteString(d.Content)
	}
	if body.String() != "ok" {
		t.Fatalf("body = %q, want ok", body.String())
	}
}

// TestEndpointBetaHeader_ScopedPerEndpoint: one gateway demanding a beta must
// not make SuperCli send it to a different one.
func TestEndpointBetaHeader_ScopedPerEndpoint(t *testing.T) {
	t.Cleanup(clearEndpointBetas)
	clearEndpointBetas()

	rememberEndpointBeta("https://anyrouter.top/v1", anthropicBetaContext1M)
	if got := endpointBetaHeader("https://anyrouter.top/v1"); got != anthropicBetaContext1M {
		t.Errorf("header for the gated endpoint = %q, want %q", got, anthropicBetaContext1M)
	}
	// Same entry, as a transport would spell it after normalization.
	if got := endpointBetaHeader("HTTPS://AnyRouter.top/v1/"); got != anthropicBetaContext1M {
		t.Errorf("header for the normalized URL = %q, want %q", got, anthropicBetaContext1M)
	}
	if got := endpointBetaHeader("https://api.anthropic.com/v1"); got != "" {
		t.Errorf("header for an unrelated endpoint = %q, want empty", got)
	}
	if !endpointRequiresBeta("https://anyrouter.top/v1", anthropicBetaContext1M) {
		t.Error("endpointRequiresBeta = false for a remembered feature")
	}
	if endpointRequiresBeta("https://api.anthropic.com/v1", anthropicBetaContext1M) {
		t.Error("endpointRequiresBeta = true for an endpoint that never asked")
	}
}
