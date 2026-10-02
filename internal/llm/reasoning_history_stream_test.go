package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestChatReasoningReplayOptionalValues(t *testing.T) {
	tests := []struct {
		name  string
		delta map[string]json.RawMessage
		want  string
	}{
		{name: "absent"},
		{name: "plain content", delta: map[string]json.RawMessage{"content": json.RawMessage("\"visible\"")}},
		{name: "present nil", delta: map[string]json.RawMessage{"reasoning_content": nil}},
		{name: "null", delta: map[string]json.RawMessage{"reasoning_content": json.RawMessage("null")}},
		{name: "empty", delta: map[string]json.RawMessage{"reasoning_content": json.RawMessage("\"\"")}},
		{name: "number", delta: map[string]json.RawMessage{"reasoning_content": json.RawMessage("17")}},
		{name: "object", delta: map[string]json.RawMessage{"reasoning_content": json.RawMessage("{\"text\":\"display only\"}")}},
		{name: "malformed", delta: map[string]json.RawMessage{"reasoning_content": json.RawMessage("\"truncated")}},
		{name: "fallback after null", delta: map[string]json.RawMessage{"reasoning_content": json.RawMessage("null"), "reasoning_text": json.RawMessage("\"exact ✓\"")}, want: "exact ✓"},
		{name: "fallback after type error", delta: map[string]json.RawMessage{"reasoning_content": json.RawMessage("{}"), "reasoning": json.RawMessage("\"exact ✓\"")}, want: "exact ✓"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var a chatReasoningAccumulator
			a.add(tt.delta)
			block := a.block("fixture", "https://fixture.example/v1")
			if tt.want == "" {
				if block != nil {
					t.Fatalf("unexpected replay: %s", block.Data)
				}
				return
			}
			if block == nil {
				t.Fatal("missing replay")
			}
			if err := block.Validate(); err != nil {
				t.Fatal(err)
			}
			var fields map[string]string
			if err := json.Unmarshal(block.Data, &fields); err != nil {
				t.Fatal(err)
			}
			field := "reasoning_text"
			if tt.name == "fallback after type error" {
				field = "reasoning"
			}
			if !reflect.DeepEqual(fields, map[string]string{field: tt.want}) {
				t.Fatalf("replay=%v", fields)
			}
		})
	}
}

func TestChatReasoningReplayRetainsSelectedFieldAcrossContent(t *testing.T) {
	var a chatReasoningAccumulator
	a.add(map[string]json.RawMessage{"reasoning_text": json.RawMessage("\"first \"")})
	a.add(map[string]json.RawMessage{"content": json.RawMessage("\"visible answer\"")})
	a.add(map[string]json.RawMessage{"reasoning_content": json.RawMessage("null")})
	// Mirrored text under another field must not switch the selected native field.
	a.add(map[string]json.RawMessage{"reasoning_content": json.RawMessage("\"mirror\"")})
	a.add(map[string]json.RawMessage{"reasoning_text": json.RawMessage("\"✓ \\uD83D\\uDE80\"")})
	block := a.block("fixture", "https://fixture.example/v1")
	if block == nil || string(block.Data) != "{\"reasoning_text\":\"first ✓ 🚀\"}" {
		t.Fatalf("replay=%+v", block)
	}
	if a.block("fixture", "https://fixture.example/v1") != nil {
		t.Fatal("duplicate replay after flush")
	}
	a.add(map[string]json.RawMessage{"reasoning": json.RawMessage("\"new turn\"")})
	next := a.block("fixture", "https://fixture.example/v1")
	if next == nil || string(next.Data) != "{\"reasoning\":\"new turn\"}" {
		t.Fatalf("next replay=%+v", next)
	}
}

func TestNativeChatReplayPreservesMixedDeltaOrderAndHistory(t *testing.T) {
	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		requests = append(requests, body)
		w.Header().Set("Content-Type", "text/event-stream")
		if len(requests) > 1 {
			io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"End\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		frames := []string{
			"{\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"Visible prefix \"}}]}",
			"{\"choices\":[{\"delta\":{\"reasoning_content\":null,\"content\":\"kept.\"}}]}",
			"{\"choices\":[{\"delta\":{\"reasoning_content\":\"plan \"}}]}",
			"{\"choices\":[{\"delta\":{\"reasoning_content\":null,\"reasoning\":null}}]}",
			"{\"choices\":[{\"delta\":{\"reasoning_content\":\"✓ exact\"}}]}",
			"{\"choices\":[{\"delta\":{\"content\":\"Answer.\",\"tool_calls\":[{\"index\":0,\"id\":\"lookup-1\",\"type\":\"function\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{\\\"q\\\":\"}}]}}]}",
			"{\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"value\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}",
			"{\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":5,\"total_tokens\":8}}",
		}
		for _, frame := range frames {
			io.WriteString(w, "data: "+frame+"\n\n")
		}
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	p, err := NewOpenAI(OpenAIConfig{BaseURL: server.URL, Model: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	user := Message{Role: RoleUser, Parts: []ContentPart{
		{Type: PartTypeText, Text: "first"},
		{Type: PartTypeImage, Image: &ImageRef{Data: "AAAA", MediaType: "image/png"}},
	}}
	tools := []ToolDef{{Name: "lookup", Description: "lookup", Schema: "{\"type\":\"object\"}"}}
	stream, err := p.Complete(context.Background(), []Message{user}, tools)
	if err != nil {
		t.Fatal(err)
	}
	assistant := Message{Role: RoleAssistant}
	var order []string
	var visible strings.Builder
	var lastUsage *Usage
	for d := range stream {
		if d.Err != nil {
			t.Fatal(d.Err)
		}
		if d.Role != "" {
			order = append(order, "role")
		}
		if d.Content != "" {
			order = append(order, "content:"+d.Content)
			visible.WriteString(d.Content)
		}
		if d.Reasoning != "" {
			order = append(order, "reasoning:"+d.Reasoning)
		}
		if d.OutputStarted {
			order = append(order, "tool-start")
		}
		if d.NativeReasoning != nil {
			order = append(order, "native")
			assistant.Parts = append(assistant.Parts, ContentPart{Type: PartTypeReasoning, Reasoning: d.NativeReasoning})
		}
		if d.ToolCall != nil {
			order = append(order, "tool")
			assistant.ToolCalls = append(assistant.ToolCalls, *d.ToolCall)
		}
		if d.FinishReason != "" {
			order = append(order, "finish:"+d.FinishReason)
		}
		if d.Usage != nil {
			order = append(order, "usage")
			lastUsage = d.Usage
		}
	}
	wantOrder := []string{"role", "content:Visible prefix ", "content:kept.", "reasoning:plan ", "reasoning:✓ exact", "content:Answer.", "tool-start", "native", "tool", "finish:tool_calls", "usage"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("delta order=%q", order)
	}
	if lastUsage == nil || *lastUsage != (Usage{Input: 3, Output: 5, Total: 8}) {
		t.Fatalf("usage=%+v", lastUsage)
	}
	if !reflect.DeepEqual(assistant.ToolCalls, []ToolCall{{ID: "lookup-1", Name: "lookup", Arguments: "{\"q\":\"value\"}"}}) {
		t.Fatalf("tools=%+v", assistant.ToolCalls)
	}
	assistant.Parts = append(assistant.Parts, ContentPart{Type: PartTypeText, Text: visible.String()})
	history := []Message{user, assistant, {Role: RoleTool, ToolCallID: "lookup-1", Content: "tool result"}}
	before, err := json.Marshal(history)
	if err != nil {
		t.Fatal(err)
	}
	next, err := p.Complete(context.Background(), history, tools)
	if err != nil {
		t.Fatal(err)
	}
	for d := range next {
		if d.Err != nil {
			t.Fatal(d.Err)
		}
	}
	after, _ := json.Marshal(history)
	if string(before) != string(after) {
		t.Fatal("archive mutated during replay")
	}
	wire := requests[1]["messages"].([]any)
	replay := wire[1].(map[string]any)
	if replay["reasoning_content"] != "plan ✓ exact" || replay["content"] != "Visible prefix kept.Answer." {
		t.Fatalf("replay=%v", replay)
	}
	tool := replay["tool_calls"].([]any)[0].(map[string]any)
	function := tool["function"].(map[string]any)
	if tool["id"] != "lookup-1" || function["name"] != "lookup" || function["arguments"] != "{\"q\":\"value\"}" {
		t.Fatalf("replayed tool=%v", tool)
	}
	result := wire[2].(map[string]any)
	if result["role"] != "tool" || result["tool_call_id"] != "lookup-1" || result["content"] != "tool result" {
		t.Fatalf("tool result=%v", result)
	}
	media := wire[0].(map[string]any)["content"].([]any)
	if len(media) != 2 || media[0].(map[string]any)["text"] != "first" || media[1].(map[string]any)["image_url"].(map[string]any)["url"] != "data:image/png;base64,AAAA" {
		t.Fatalf("media=%v", media)
	}
	if strings.Contains(replay["content"].(string), "plan") {
		t.Fatal("native reasoning leaked into answer")
	}
}
