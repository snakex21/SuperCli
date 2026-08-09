package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestBuildAnthropicRequest_TextToolsThinking(t *testing.T) {
	t.Cleanup(func() { _ = SetReasoningEffort(""); clearReasoningEffortSupport() })
	if err := SetReasoningEffort("low"); err != nil {
		t.Fatal(err)
	}
	body, err := buildAnthropicRequest("claude-sonnet-4-5", []Message{
		{Role: RoleSystem, Content: "you are helpful"},
		{Role: RoleUser, Content: "hi"},
	}, []ToolDef{{Name: "read_file", Description: "Read", Schema: `{"path":{"type":"string"}}`}}, true, 4096)
	if err != nil {
		t.Fatal(err)
	}
	var req anthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	if req.Model != "claude-sonnet-4-5" || req.System != "you are helpful" || len(req.Messages) != 1 {
		t.Fatalf("bad request: %+v", req)
	}
	if req.Thinking == nil || req.Thinking.Type != "enabled" || req.Thinking.BudgetTokens <= 0 {
		t.Fatalf("thinking = %+v, want enabled budget", req.Thinking)
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "read_file" {
		t.Fatalf("tools = %+v", req.Tools)
	}
}

func TestBuildAnthropicRequest_VisionDisabledReplacesImage(t *testing.T) {
	body, err := buildAnthropicRequest("text-only", []Message{{
		Role: RoleUser,
		Parts: []ContentPart{
			{Type: PartTypeText, Text: "look"},
			{Type: PartTypeImage, Image: &ImageRef{Data: "AAAA", MediaType: "image/png"}},
		},
	}}, nil, false, 4096)
	if err != nil {
		t.Fatal(err)
	}
	var req anthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 1 || len(req.Messages[0].Content) != 2 {
		t.Fatalf("unexpected messages: %+v", req.Messages)
	}
	if got := req.Messages[0].Content[1]; got.Type != "text" || got.Text != imageInputOmittedPlaceholder {
		t.Fatalf("image replacement = %+v", got)
	}
}

func TestAnthropic_UnknownVisionRejectionRetriesAndLearns(t *testing.T) {
	var requests []string
	var captured *string
	srv, captured := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, *captured)
		if len(requests) == 1 {
			w.WriteHeader(422)
			_, _ = w.Write([]byte(`{"error":{"message":"model does not support image input"}}`))
			return
		}
		sseResponse(w,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
			`{"type":"message_stop"}`,
		)
	})
	p, err := NewAnthropic(AnthropicConfig{BaseURL: srv.URL, APIKey: "key", Model: "custom-model"})
	if err != nil {
		t.Fatal(err)
	}
	msgs := []Message{{Role: RoleUser, Parts: []ContentPart{
		{Type: PartTypeText, Text: "look"},
		{Type: PartTypeImage, Image: &ImageRef{Data: "AAAA", MediaType: "image/png"}},
	}}}
	ch, err := p.Complete(context.Background(), msgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	var sawNotice bool
	for _, d := range drainDeltas(t, ch) {
		if d.Err != nil {
			t.Fatalf("unexpected error: %v", d.Err)
		}
		if d.Notice != "" {
			sawNotice = true
		}
	}
	if !sawNotice || len(requests) != 2 {
		t.Fatalf("retry notice=%v requests=%d", sawNotice, len(requests))
	}
	if !strings.Contains(requests[0], `"type":"image"`) {
		t.Fatalf("first request did not try image input: %s", requests[0])
	}
	if strings.Contains(requests[1], `"type":"image"`) || !strings.Contains(requests[1], imageInputOmittedPlaceholder) {
		t.Fatalf("fallback request did not replace image: %s", requests[1])
	}

	// The rejection is remembered only by this provider instance, so the next
	// turn skips the doomed image attempt and makes exactly one request.
	ch, err = p.Complete(context.Background(), msgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range drainDeltas(t, ch) {
		if d.Err != nil {
			t.Fatalf("unexpected learned-path error: %v", d.Err)
		}
	}
	if len(requests) != 3 || strings.Contains(requests[2], `"type":"image"`) || !strings.Contains(requests[2], imageInputOmittedPlaceholder) {
		t.Fatalf("learned text-only path wrong: %+v", requests)
	}
}

func TestNormalizeAnthropicBaseURL(t *testing.T) {
	cases := map[string]string{
		"https://api.anthropic.com/v1":           "https://api.anthropic.com/v1",
		"https://api.anthropic.com/v1/":          "https://api.anthropic.com/v1",
		"https://api.anthropic.com/v1/messages":  "https://api.anthropic.com/v1",
		"https://api.anthropic.com/v1/messages/": "https://api.anthropic.com/v1",
		"https://anyrouter.top/v1/messages":      "https://anyrouter.top/v1",
		"  https://anyrouter.top/v1/messages  ":  "https://anyrouter.top/v1",
	}
	for in, want := range cases {
		if got := NormalizeAnthropicBaseURL(in); got != want {
			t.Errorf("NormalizeAnthropicBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestAnthropic_BaseURLWithMessagesSuffix guards the "endpoint doesn't work"
// bug: users paste the full ".../v1/messages" endpoint as the provider base
// URL, and the request must still hit ".../v1/messages" — not the doubled
// ".../v1/messages/messages".
func TestAnthropic_BaseURLWithMessagesSuffix(t *testing.T) {
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path=%s want /v1/messages (base URL /messages suffix must not double)", r.URL.Path)
		}
		sseResponse(w,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
			`{"type":"message_stop"}`,
		)
	})
	p, err := NewAnthropic(AnthropicConfig{BaseURL: srv.URL + "/v1/messages", APIKey: "key", Model: "claude-opus-4-8"})
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
		t.Fatalf("body=%q want ok", body.String())
	}
}

func TestAnthropic_CompleteHeadersAndText(t *testing.T) {
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			t.Fatalf("path=%s want /messages", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "key" {
			t.Fatalf("x-api-key header missing")
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Fatalf("anthropic-version header missing")
		}
		sseResponse(w,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Cześć"}}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
			`{"type":"message_stop"}`,
		)
	})
	p, err := NewAnthropic(AnthropicConfig{BaseURL: srv.URL, APIKey: "key", Model: "claude-sonnet"})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ds := drainDeltas(t, ch)
	var body strings.Builder
	var finish string
	for _, d := range ds {
		body.WriteString(d.Content)
		if d.FinishReason != "" {
			finish = d.FinishReason
		}
	}
	if body.String() != "Cześć" || finish != "stop" {
		t.Fatalf("body=%q finish=%q", body.String(), finish)
	}
}

// TestAnthropic_UsageCachePromptBreakdown: message_start carries the
// input-side usage including the prompt-cache split
// (cache_read_input_tokens / cache_creation_input_tokens), message_delta
// the output side. Input must fold the cache read+creation tokens in
// (Anthropic reports input_tokens EXCLUSIVE of cache activity) so the
// cache-hit denominator matches the OpenAI convention, and CachedInput
// must carry the cache-read share for the per-turn cache-miss line.
func TestAnthropic_UsageCachePromptBreakdown(t *testing.T) {
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		sseResponse(w,
			`{"type":"message_start","message":{"usage":{"input_tokens":11,"cache_creation_input_tokens":2000,"cache_read_input_tokens":12000,"output_tokens":1}}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":220}}`,
			`{"type":"message_stop"}`,
		)
	})
	p, err := NewAnthropic(AnthropicConfig{BaseURL: srv.URL, APIKey: "key", Model: "claude-sonnet"})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var usage *Usage
	for _, d := range drainDeltas(t, ch) {
		if d.Usage != nil {
			usage = d.Usage
		}
	}
	if usage == nil {
		t.Fatal("expected usage on the final delta")
	}
	if usage.Input != 14011 {
		t.Errorf("Input = %d, want 14011 (11 + 2000 creation + 12000 read)", usage.Input)
	}
	if usage.CachedInput != 12000 {
		t.Errorf("CachedInput = %d, want 12000 (cache_read_input_tokens)", usage.CachedInput)
	}
	if usage.Output != 220 || usage.Total != 14231 {
		t.Errorf("Output/Total = %d/%d, want 220/14231", usage.Output, usage.Total)
	}
}

// TestAnthropic_UsageWithoutCacheFields: plain usage (no cache fields,
// e.g. proxies or requests without cache_control) must still produce
// input+output accounting with zero CachedInput — missing telemetry is
// not an error.
func TestAnthropic_UsageWithoutCacheFields(t *testing.T) {
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		sseResponse(w,
			`{"type":"message_start","message":{"usage":{"input_tokens":300,"output_tokens":1}}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`,
			`{"type":"message_stop"}`,
		)
	})
	p, err := NewAnthropic(AnthropicConfig{BaseURL: srv.URL, APIKey: "key", Model: "claude-sonnet"})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var usage *Usage
	for _, d := range drainDeltas(t, ch) {
		if d.Usage != nil {
			usage = d.Usage
		}
	}
	if usage == nil {
		t.Fatal("expected usage")
	}
	if usage.Input != 300 || usage.Output != 7 || usage.CachedInput != 0 {
		t.Errorf("usage = %+v, want 300 in / 7 out / 0 cached", usage)
	}
}

func TestAnthropic_StreamThinking(t *testing.T) {
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		sseResponse(w,
			`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Plan"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"\nAnswer"}}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
			`{"type":"message_stop"}`,
		)
	})
	p, _ := NewAnthropic(AnthropicConfig{BaseURL: srv.URL, APIKey: "key", Model: "claude-sonnet"})
	ch, _ := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	ds := drainDeltas(t, ch)
	var body strings.Builder
	for _, d := range ds {
		body.WriteString(d.Content)
	}
	if body.String() != "<thinking>Plan</thinking>\nAnswer" {
		t.Fatalf("body=%q", body.String())
	}
}

func TestAnthropic_StreamToolUse(t *testing.T) {
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		sseResponse(w,
			`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"read_file","input":{}}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a.go\"}"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
			`{"type":"message_stop"}`,
		)
	})
	p, _ := NewAnthropic(AnthropicConfig{BaseURL: srv.URL, APIKey: "key", Model: "claude-sonnet"})
	ch, _ := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "use tool"}}, nil)
	ds := drainDeltas(t, ch)
	var tc *ToolCall
	var finish string
	for _, d := range ds {
		if d.ToolCall != nil {
			tc = d.ToolCall
		}
		if d.FinishReason != "" {
			finish = d.FinishReason
		}
	}
	if tc == nil || tc.ID != "toolu_1" || tc.Name != "read_file" || tc.Arguments != `{"path":"a.go"}` {
		t.Fatalf("tool call=%+v", tc)
	}
	if finish != "tool_calls" {
		t.Fatalf("finish=%q want tool_calls", finish)
	}
}

func TestAnthropicMalformedAndIncompleteStreamsAreErrors(t *testing.T) {
	tests := []struct {
		name   string
		chunks []string
		want   string
	}{
		{name: "malformed", chunks: []string{`not json`}, want: "malformed SSE payload"},
		{name: "incomplete", chunks: []string{`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`}, want: "before message_stop"},
		{name: "structured error", chunks: []string{`{"type":"error","error":{"message":"overloaded"}}`}, want: "overloaded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) { sseResponse(w, tt.chunks...) })
			provider, err := NewAnthropic(AnthropicConfig{BaseURL: srv.URL, APIKey: "key", Model: "claude"})
			if err != nil {
				t.Fatal(err)
			}
			deltas, err := provider.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			var streamErr error
			for delta := range deltas {
				if delta.Err != nil {
					streamErr = delta.Err
				}
			}
			if streamErr == nil || !strings.Contains(streamErr.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", streamErr, tt.want)
			}
		})
	}
}
