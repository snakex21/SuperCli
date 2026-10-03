package session

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestToolImageProvenanceDurableRoundTrip(t *testing.T) {
	home := t.TempDir()
	store, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(home, "fixture", "host image")
	if err != nil {
		t.Fatal(err)
	}
	writer := NewWriter(store, sess.ID)
	ref, err := writer.ExternalizeImage(context.Background(), "image/png", []byte("synthetic pixels"))
	if err != nil {
		t.Fatal(err)
	}
	ref.SourceToolCallID, ref.ToolOutputCarrier, ref.Active = "exact-call-α", true, true
	msg := llm.Message{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: "Host text remains exact"}, {Type: llm.PartTypeImage, Image: &ref}}}
	encoded, err := FromMessage(msg)
	if err != nil {
		t.Fatal(err)
	}
	back, err := encoded.ToMessage()
	if err != nil || !reflect.DeepEqual(msg, back) {
		t.Fatalf("codec roundtrip: %+v %v", back, err)
	}
	if !strings.Contains(encoded.PartsJSON, "source_tool_call_id") || !strings.Contains(encoded.PartsJSON, "tool_output_carrier") {
		t.Fatal("codec lost optional origin")
	}
	if err := writer.AppendMessage(context.Background(), msg.DormantImages()); err != nil {
		t.Fatal(err)
	}
	if err := writer.SaveContextProjection(context.Background(), []llm.Message{msg.DormantImages()}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	rows, err := store.ReadMessages(context.Background(), sess.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("reopen rows: %+v %v", rows, err)
	}
	restored, err := rows[0].ToMessage()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored, msg.DormantImages()) || restored.ToolCallID != "" || restored.Role != llm.RoleUser {
		t.Fatal("storage changed role/text/origin or retained active pixels")
	}
	projection, err := store.ReadModelContext(context.Background(), sess.ID)
	if err != nil || len(projection) != 1 || !reflect.DeepEqual(projection[0], restored) {
		t.Fatal("resume projection lost exact provenance")
	}
	if !ref.Active {
		t.Fatal("persistence mutated original active reference")
	}
	ordinary := ref
	ordinary.SourceToolCallID, ordinary.ToolOutputCarrier = "", false
	raw, _ := json.Marshal(ordinary)
	if strings.Contains(string(raw), "source_tool_call_id") || strings.Contains(string(raw), "tool_output_carrier") {
		t.Fatal("optional fields were added to genuine attachment encoding")
	}
}
