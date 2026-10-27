package webgui

import (
	"context"
	"reflect"
	"testing"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

// MCP image_mode=preview thumbnails have no model-visible carrier; they are
// restored from the tool result record when an old session is reopened.
func TestTranscriptRestoresToolPreviewThumbnails(t *testing.T) {
	srv := newTestServer(t, false)
	ctx := context.Background()
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(srv.eng.Home(), "fixture", "previews")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, sess.ID)
	ref, err := writer.ExternalizeImage(ctx, "image/png", []byte("preview pixels"))
	if err != nil {
		t.Fatal(err)
	}
	call := llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "mcp_bridge", Arguments: "{}"}}}
	result := llm.Message{Role: llm.RoleTool, ToolCallID: "c1", Name: "mcp_bridge", Content: "[image saved: x]",
		Origin: &llm.ContextOrigin{Source: llm.SourceToolResult, ToolPreviews: []string{ref.Path, "/etc/passwd"}}}
	forged := llm.Message{Role: llm.RoleUser, Content: "hi", Origin: &llm.ContextOrigin{Source: llm.SourceHuman, ToolPreviews: []string{ref.Path}}}
	for _, msg := range []llm.Message{forged, call, result} {
		if err := writer.AppendMessage(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := srv.eng.transcript(ctx, sess.ID)
	if err != nil || len(rows) != 3 {
		t.Fatalf("transcript: %+v %v", rows, err)
	}
	want := []string{sessionImagePreviewPath(sess.ID, ref.Path)}
	if !reflect.DeepEqual(rows[2].ToolPreviews, want) {
		t.Fatalf("tool previews = %+v, want %+v", rows[2].ToolPreviews, want)
	}
	if len(rows[0].ToolPreviews) != 0 {
		t.Fatal("non-tool message exposed previews")
	}
}

func TestToolProgressWireEvent(t *testing.T) {
	w, ok := toWireEvent(agent.ToolProgressEvent{ID: "c1", Text: "50% · step"})
	if !ok || w.Type != "tool_progress" || w.ID != "c1" || w.Text != "50% · step" {
		t.Fatalf("wire = %+v ok=%v", w, ok)
	}
}
