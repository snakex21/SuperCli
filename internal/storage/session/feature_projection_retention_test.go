package session

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestReadModelContextPreservesTailMetadata(t *testing.T) {
	for _, mode := range []string{"transcript", "projection", "corrupt projection"} {
		t.Run(mode, func(t *testing.T) {
			store := openTestStore(t)
			ctx := context.Background()
			sess, err := store.Create(t.TempDir(), "fixture-model", "")
			if err != nil {
				t.Fatal(err)
			}
			image, err := NewWriter(store, sess.ID).ExternalizeImage(ctx, "image/png", []byte("persisted image bytes"))
			if err != nil {
				t.Fatal(err)
			}
			image.Name = "result.png"
			messages := []llm.Message{
				{Role: llm.RoleUser, Content: "inspect"},
				{Role: llm.RoleAssistant, Content: "starting", Name: "reader", Parts: []llm.ContentPart{{Type: llm.PartTypeReasoning, Reasoning: &llm.ReasoningBlock{Format: llm.ReasoningResponses, Model: "fixture-model", Scope: "fixture-origin", Data: json.RawMessage(`{"type":"reasoning","encrypted_content":"signed-state"}`), Tokens: 47, Prefix: "exact native prefix"}}}, ToolCalls: []llm.ToolCall{{ID: "read-call", Name: "read_file", Arguments: `{"path":"missing.txt"}`}}},
				{Role: llm.RoleTool, ToolCallID: "read-call", Name: "read_file", Content: "error: file does not exist"},
				{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "image-call", Name: "capture_image", Arguments: `{"name":"result.png"}`}}},
				{Role: llm.RoleTool, ToolCallID: "image-call", Name: "capture_image", Content: "captured", Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: "image evidence"}, {Type: llm.PartTypeImage, Image: &image}}},
				{Role: llm.RoleAssistant, Content: "finished with one missing-file error"},
			}
			appendMessage := func(m llm.Message) {
				t.Helper()
				row, err := FromMessage(m)
				if err != nil {
					t.Fatal(err)
				}
				if err := store.AppendMessage(ctx, sess.ID, row); err != nil {
					t.Fatal(err)
				}
			}
			for _, m := range messages[:2] {
				appendMessage(m)
			}
			want := messages
			if mode != "transcript" {
				projection := []llm.Message{{Role: llm.RoleUser, Content: "earlier context summary"}, messages[1]}
				if err := store.SaveContextProjection(ctx, sess.ID, projection); err != nil {
					t.Fatal(err)
				}
				if mode == "corrupt projection" {
					if _, err := store.db.Exec(`UPDATE session_context_projections SET messages_json=? WHERE session_id=?`, []byte("{"), sess.ID); err != nil {
						t.Fatal(err)
					}
				} else {
					want = append(projection, messages[2:]...)
				}
			}
			for _, m := range messages[2:] {
				appendMessage(m)
			}
			before, err := store.ReadMessages(ctx, sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			got, err := store.ReadModelContext(ctx, sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("model context changed:\n got %#v\nwant %#v", got, want)
			}
			after, err := store.ReadMessages(ctx, sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("model-context conversion mutated the archived transcript")
			}
		})
	}
}

func TestReadModelContextKeepsFirstTailDecodeError(t *testing.T) {
	for _, first := range []string{"parts", "tool_calls"} {
		t.Run(first, func(t *testing.T) {
			store := openTestStore(t)
			ctx := context.Background()
			sess, err := store.Create(t.TempDir(), "fixture-model", "")
			if err != nil {
				t.Fatal(err)
			}
			row, err := FromMessage(llm.Message{Role: llm.RoleUser, Content: "prior"})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.AppendMessage(ctx, sess.ID, row); err != nil {
				t.Fatal(err)
			}
			if err := store.SaveContextProjection(ctx, sess.ID, []llm.Message{{Role: llm.RoleUser, Content: "prior"}}); err != nil {
				t.Fatal(err)
			}
			badParts := Encoded{Role: string(llm.RoleAssistant), Content: "parts error", PartsJSON: "{"}
			badCalls := Encoded{Role: string(llm.RoleAssistant), Content: "calls error", ToolCallsJSON: "{"}
			rows := []Encoded{badParts, badCalls}
			if first == "tool_calls" {
				rows[0], rows[1] = rows[1], rows[0]
			}
			for _, row := range rows {
				if err := store.AppendMessage(ctx, sess.ID, row); err != nil {
					t.Fatal(err)
				}
			}
			before, err := store.ReadMessages(ctx, sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			got, err := store.ReadModelContext(ctx, sess.ID)
			if got != nil || err == nil || !strings.Contains(err.Error(), "ToMessage: "+first+":") {
				t.Fatalf("context=%+v,error=%v; want first %s error", got, err, first)
			}
			after, err := store.ReadMessages(ctx, sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("failed resume mutated rows")
			}
		})
	}
}
