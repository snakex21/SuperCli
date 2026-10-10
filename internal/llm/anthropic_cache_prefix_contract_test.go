package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// These are serializer/transport contracts, not inference or cache-hit tests.
// The transport returns explicitly synthetic SSE without contacting a backend.
func TestAnthropicCacheFirstTurnStableSystemFallback(t *testing.T) {
	tools := []ToolDef{{Name: "read_file", Description: "Read source", Schema: `{"type":"object","properties":{"path":{"type":"string"}}}`}}
	for _, reminder := range []string{"Current time: 12:00", "Current time: 12:01"} {
		msgs := []Message{
			{Role: RoleSystem, Content: "Preserve C:\\projekt łódź\\src.js and test fixtures.\nDo not publish."},
			{Role: RoleUser, Content: "Find and repair the bug; keep selected reasoning."},
			{Role: RoleSystem, Content: reminder},
		}
		legacy, err := buildAnthropicRequestWithSampling("claude-cache-fixture", msgs, tools, true, 8192, Sampling{})
		if err != nil {
			t.Fatal(err)
		}
		cached, err := buildAnthropicRequestWithCaching("claude-cache-fixture", msgs, tools, true, 8192, Sampling{}, true)
		if err != nil {
			t.Fatal(err)
		}
		var got, want map[string]any
		if err := json.Unmarshal(cached, &got); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(legacy, &want); err != nil {
			t.Fatal(err)
		}
		blocks, ok := got["system"].([]any)
		if !ok || len(blocks) != 1 {
			t.Fatalf("expected one stable system text block, got %#v", got["system"])
		}
		block := blocks[0].(map[string]any)
		if block["type"] != "text" || block["text"] != msgs[0].Content ||
			!reflect.DeepEqual(block["cache_control"], map[string]any{"type": "ephemeral"}) {
			t.Fatalf("system fallback changed text or cache metadata: %#v", block)
		}
		if bytes.Count(cached, []byte(`"cache_control"`)) != 1 {
			t.Fatal("fallback must add exactly one marker, before the changing tail")
		}
		got["system"] = block["text"]
		if !reflect.DeepEqual(got, want) {
			t.Fatal("cache fallback changed facts, tools, sampling, thinking or output budget")
		}
		if anthropicRequestPrefix(cached) == "" || anthropicRequestPrefix(cached) != anthropicRequestPrefix(legacy) {
			t.Fatal("system block cache changed the legacy content prefix")
		}
		t.Logf("first-turn cache addition: %d bytes; all prompt text and tool schemas preserved", len(cached)-len(legacy))
	}
}

func TestAnthropicCacheFirstTurnScopeAndSubsequent(t *testing.T) {
	for _, tc := range []struct {
		name, base, purpose, system string
		assistant                   bool
		wantSystem                  bool
		wantMessage                 bool
	}{
		{"main", "https://api.anthropic.com/v1", PurposeMain, "stable", false, true, false},
		{"task", "https://api.anthropic.com/v1", PurposeTask, "stable", false, true, false},
		{"subsequent", "https://api.anthropic.com/v1", PurposeMain, "stable", true, false, true},
		{"helper", "https://api.anthropic.com/v1", PurposeCompact, "stable", false, false, false},
		{"gateway", "https://proxy.invalid/v1", PurposeMain, "stable", false, false, false},
		{"empty_system", "https://api.anthropic.com/v1", PurposeMain, "", false, false, false},
		{"blank_system", "https://api.anthropic.com/v1", PurposeMain, " \n\t", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msgs := []Message{{Role: RoleUser, Content: "real user instruction"}}
			if tc.system != "" {
				msgs = append([]Message{{Role: RoleSystem, Content: tc.system}}, msgs...)
			}
			if tc.assistant {
				msgs = append(msgs, Message{Role: RoleAssistant, Content: "completed step"})
			}
			msgs = append(msgs, Message{Role: RoleSystem, Content: "changing reminder"})
			legacy, err := buildAnthropicRequestWithSampling("claude-cache-fixture", msgs, nil, true, 4096, Sampling{})
			if err != nil {
				t.Fatal(err)
			}
			var sent []byte
			calls := 0
			p, err := NewAnthropic(AnthropicConfig{BaseURL: tc.base, Model: "claude-cache-fixture", HTTPClient: &http.Client{Transport: anthropicOwnedBodyTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				sent, err = io.ReadAll(req.Body)
				return anthropicOwnedBodyResponse(req, http.StatusOK, "data: {\"type\":\"message_stop\"}\n\n"), err
			})}})
			if err != nil {
				t.Fatal(err)
			}
			ch, err := p.Complete(WithPurpose(context.Background(), tc.purpose), msgs, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range drainDeltas(t, ch) {
				if d.Err != nil {
					t.Fatal(d.Err)
				}
			}
			if calls != 1 {
				t.Fatalf("cache placement caused %d requests", calls)
			}
			var req struct {
				System   json.RawMessage
				Messages []anthropicMessage
			}
			if err := json.Unmarshal(sent, &req); err != nil {
				t.Fatal(err)
			}
			systemMarker := strings.Contains(string(req.System), `"cache_control"`)
			messageMarker := false
			for _, m := range req.Messages {
				for _, b := range m.Content {
					messageMarker = messageMarker || b.CacheControl != nil
				}
			}
			if systemMarker != tc.wantSystem || messageMarker != tc.wantMessage {
				t.Fatalf("markers: system=%v message=%v", systemMarker, messageMarker)
			}
			if !tc.wantSystem && !tc.wantMessage && !bytes.Equal(sent, legacy) {
				t.Fatal("disabled/gateway/helper/empty-system wire changed")
			}
			if req.Messages[len(req.Messages)-1].Content[0].CacheControl != nil {
				t.Fatal("changing final reminder acquired a marker")
			}
			if anthropicRequestPrefix(sent) != anthropicRequestPrefix(legacy) {
				t.Fatal("cache placement changed canonical prefix")
			}
		})
	}
}

func TestAnthropicCacheFirstTurnSignedContinuation(t *testing.T) {
	const model = "claude-cache-fixture"
	initial := []Message{{Role: RoleSystem, Content: "stable instructions"}, {Role: RoleUser, Content: "user task"}, {Role: RoleSystem, Content: "old reminder"}}
	legacy, err := buildAnthropicRequestWithSampling(model, initial, nil, true, 4096, Sampling{})
	if err != nil {
		t.Fatal(err)
	}
	cached, err := buildAnthropicRequestWithCaching(model, initial, nil, true, 4096, Sampling{}, true)
	if err != nil {
		t.Fatal(err)
	}
	// Synthetic signed blocks exercise replay; no model reasoning is generated.
	raw := []byte(`{"type":"assistant","content":[{"type":"thinking","thinking":"synthetic fixture","signature":"synthetic-signature","future":{"keep":true}},{"type":"text","text":"observed result"}]}`)
	for _, prefix := range []string{anthropicRequestPrefix(legacy), anthropicRequestPrefix(cached)} {
		native := nativeReasoning(ReasoningAnthropic, model, "https://api.anthropic.com/v1", raw)
		native.Prefix = prefix
		next := append(append([]Message{}, initial...),
			Message{Role: RoleAssistant, Parts: []ContentPart{{Type: PartTypeReasoning, Reasoning: native}}},
			Message{Role: RoleUser, Content: "continue"}, Message{Role: RoleSystem, Content: "new reminder"})
		body, err := buildAnthropicRequestWithCaching(model, next, nil, true, 4096, Sampling{}, true)
		if err != nil {
			t.Fatal(err)
		}
		var req struct {
			Messages []struct{ Content []json.RawMessage }
		}
		var original struct{ Content []json.RawMessage }
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &original); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(req.Messages[1].Content, original.Content) || !bytes.Equal(native.Data, raw) {
			t.Fatal("first-turn system marker invalidated or edited signed continuation")
		}
		uncached, err := buildAnthropicRequestWithSampling(model, next, nil, true, 4096, Sampling{})
		if err != nil || anthropicRequestPrefix(body) != anthropicRequestPrefix(uncached) {
			t.Fatal("subsequent prefix must still match legacy content")
		}
	}
}

func TestAnthropicCacheSystemPrefixNormalizationIsNarrow(t *testing.T) {
	for _, raw := range []string{
		`[]`, `[{"type":"text","text":"one"},{"type":"text","text":"two"}]`,
		`[{"type":"image","text":"one"}]`, `[{"type":"text","text":null}]`,
		`[{"type":"text","text":"one","future_semantics":true}]`,
	} {
		if anthropicRequestPrefix([]byte(`{"system":`+raw+`,"messages":[]}`)) != "" {
			t.Fatalf("unrecognized native system shape normalized as plain text: %s", raw)
		}
	}
}
