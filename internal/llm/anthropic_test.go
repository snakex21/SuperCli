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
	}, []ToolDef{{Name: "read_file", Description: "Read", Schema: `{"path":{"type":"string"}`}}, true, 4096)
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
