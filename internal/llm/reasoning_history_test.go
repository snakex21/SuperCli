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

func collectReasoning(t *testing.T, p Provider, messages []Message) Message {
	t.Helper()
	ch, err := p.Complete(context.Background(), messages, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := Message{Role: RoleAssistant}
	for d := range ch {
		if d.Err != nil {
			t.Fatal(d.Err)
		}
		if d.NativeReasoning != nil {
			if err := d.NativeReasoning.Validate(); err != nil {
				t.Fatal(err)
			}
			m.Parts = append(m.Parts, ContentPart{Type: PartTypeReasoning, Reasoning: d.NativeReasoning})
		}
		if d.ToolCall != nil {
			m.ToolCalls = append(m.ToolCalls, *d.ToolCall)
		}
		if d.Content != "" {
			m.Parts = append(m.Parts, ContentPart{Type: PartTypeText, Text: d.Content})
		}
	}
	return m
}

func TestNativeChatReasoningRoundTrip(t *testing.T) {
	for _, field := range []string{"reasoning_content", "reasoning", "reasoning_text"} {
		t.Run(field, func(t *testing.T) {
			var requests []map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				requests = append(requests, body)
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: {\"choices\":[{\"delta\":{\""+field+"\":\"keep \"}}]}\n\n")
				io.WriteString(w, "data: {\"choices\":[{\"delta\":{\""+field+"\":\"the invariant\"}}]}\n\n")
				io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer server.Close()
			p, err := NewOpenAI(OpenAIConfig{BaseURL: server.URL, Model: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			first := []Message{{Role: RoleUser, Content: "first"}}
			assistant := collectReasoning(t, p, first)
			if len(assistant.Parts) != 2 {
				t.Fatalf("parts=%+v", assistant.Parts)
			}
			before, _ := json.Marshal(assistant)
			second := append(first, assistant, Message{Role: RoleUser, Content: "continue"})
			collectReasoning(t, p, second)
			wire := requests[1]["messages"].([]any)[1].(map[string]any)
			if wire[field] != "keep the invariant" || wire["content"] != "Done" {
				t.Fatalf("wire=%v", wire)
			}
			other, _ := NewOpenAI(OpenAIConfig{BaseURL: server.URL, Model: "other"})
			collectReasoning(t, other, second)
			wire = requests[2]["messages"].([]any)[1].(map[string]any)
			if _, ok := wire[field]; ok {
				t.Fatal("native state crossed model boundary")
			}
			after, _ := json.Marshal(assistant)
			if string(before) != string(after) {
				t.Fatal("original archive mutated")
			}
		})
	}
}

func TestNativeResponsesReasoningRoundTrip(t *testing.T) {
	const raw = "{\"type\":\"reasoning\",\"id\":\"rs_fixture\",\"summary\":[],\"encrypted_content\":\"opaque-not-display-text\",\"future_field\":{\"keep\":true}}"
	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		requests = append(requests, body)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.output_item.done\",\"item\":"+raw+"}\n\n")
		io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"Done\"}\n\n")
		io.WriteString(w, "data: {\"type\":\"response.completed\"}\n\n")
	}))
	defer server.Close()
	p, err := NewResponses(ResponsesConfig{BaseURL: server.URL, Model: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	first := []Message{{Role: RoleUser, Content: "first"}}
	assistant := collectReasoning(t, p, first)
	if len(assistant.Parts) != 2 || string(assistant.Parts[0].Reasoning.Data) != raw {
		t.Fatal("opaque reasoning was lost or rewritten")
	}
	collectReasoning(t, p, append(first, assistant, Message{Role: RoleUser, Content: "continue"}))
	items := requests[1]["input"].([]any)
	var want map[string]any
	_ = json.Unmarshal([]byte(raw), &want)
	if !reflect.DeepEqual(items[1], want) {
		t.Fatalf("native item not replayed: %v", items)
	}
	if strings.Contains(assistant.TextOnly().Content, "opaque") {
		t.Fatal("opaque state leaked into UI text")
	}
}

func TestNativeAnthropicReasoningRoundTrip(t *testing.T) {
	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		requests = append(requests, body)
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(s string) { _, _ = io.WriteString(w, "data: "+s+"\n\n") }
		send("{\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\",\"signature\":\"\",\"extra\":\"preserve\"}}")
		send("{\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"plan\"}}")
		send("{\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"sig-\"}}")
		send("{\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"complete\"}}")
		send("{\"type\":\"content_block_stop\",\"index\":0}")
		send("{\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}")
		send("{\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"Done\"}}")
		send("{\"type\":\"content_block_stop\",\"index\":1}")
		send("{\"type\":\"content_block_start\",\"index\":2,\"content_block\":{\"type\":\"redacted_thinking\",\"data\":\"opaque\"}}")
		send("{\"type\":\"content_block_stop\",\"index\":2}")
		send("{\"type\":\"content_block_start\",\"index\":3,\"content_block\":{\"type\":\"tool_use\",\"id\":\"lookup-1\",\"name\":\"lookup\",\"input\":{}}}")
		send("{\"type\":\"content_block_delta\",\"index\":3,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"key\\\":\\\"fixture\\\"}\"}}")
		send("{\"type\":\"content_block_stop\",\"index\":3}")
		send("{\"type\":\"message_stop\"}")
	}))
	defer server.Close()
	p, err := NewAnthropic(AnthropicConfig{BaseURL: server.URL, Model: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	first := []Message{{Role: RoleUser, Content: "first"}}
	assistant := collectReasoning(t, p, first)
	history := append(first, assistant, Message{Role: RoleTool, ToolCallID: "lookup-1", Content: "fixture value"})
	collectReasoning(t, p, history)
	blocks := requests[1]["messages"].([]any)[1].(map[string]any)["content"].([]any)
	thinking := blocks[0].(map[string]any)
	text := blocks[1].(map[string]any)
	redacted := blocks[2].(map[string]any)
	call := blocks[3].(map[string]any)
	if thinking["thinking"] != "plan" || thinking["signature"] != "sig-complete" || thinking["extra"] != "preserve" || redacted["data"] != "opaque" ||
		text["text"] != "Done" || call["input"].(map[string]any)["key"] != "fixture" {
		t.Fatalf("native order or payload changed: %v", blocks)
	}
	original, _ := json.Marshal(assistant)
	// A pruned/changed prefix must not send an invalid signature. Visible
	// evidence and tool protocol survive, with no mutation of the archive.
	history[0].Content = "changed history"
	collectReasoning(t, p, history)
	blocks = requests[2]["messages"].([]any)[1].(map[string]any)["content"].([]any)
	if len(blocks) != 2 || blocks[0].(map[string]any)["text"] != "Done" || blocks[1].(map[string]any)["type"] != "tool_use" {
		t.Fatalf("prefix change lost visible/tool content: %v", blocks)
	}
	after, _ := json.Marshal(assistant)
	if string(original) != string(after) {
		t.Fatal("archive modified")
	}
	// Tool discovery also changes the bound prefix.
	history[0].Content = "first"
	ch, err := p.Complete(context.Background(), history, []ToolDef{{Name: "lookup", Description: "lookup", Schema: "{\"type\":\"object\"}"}})
	if err != nil {
		t.Fatal(err)
	}
	for d := range ch {
		if d.Err != nil {
			t.Fatal(d.Err)
		}
	}
	blocks = requests[3]["messages"].([]any)[1].(map[string]any)["content"].([]any)
	if len(blocks) != 2 {
		t.Fatal("changed tool set retained invalid signature")
	}
}

func TestNativeReasoningScopeAndAccounting(t *testing.T) {
	b := nativeReasoning(ReasoningResponses, "fixture", "https://a.example/v1", []byte("{\"type\":\"reasoning\",\"encrypted_content\":\"opaque\"}"))
	b.Tokens = 100
	original := []Message{{Role: RoleAssistant, Parts: []ContentPart{{Type: PartTypeReasoning, Reasoning: b}, {Type: PartTypeText, Text: "Done"}}}}
	plain := []Message{{Role: RoleAssistant, Parts: []ContentPart{{Type: PartTypeText, Text: "Done"}}}}
	if EstimateTokens(original)-EstimateTokens(plain) != 100 {
		t.Fatal("native reasoning missing from context budget")
	}
	filtered := filterNativeReasoning(original, ReasoningResponses, "fixture", "https://b.example/v1")
	if !reflect.DeepEqual(filtered, plain) || len(original[0].Parts) != 2 {
		t.Fatal("endpoint scope mismatch did not produce an independent plain view")
	}
	for _, format := range []string{ReasoningChat, ReasoningAnthropic} {
		if got := filterNativeReasoning(original, format, "fixture", "https://a.example/v1"); !reflect.DeepEqual(got, plain) {
			t.Fatal("native reasoning crossed protocol boundary")
		}
	}
}
