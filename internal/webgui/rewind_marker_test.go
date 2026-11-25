package webgui

import (
	"context"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

// Audited #5119-5123: five identical rewind notices in a row, all sent to
// the model. A new marker replaces a trailing one.
func TestRewindMarkerReplacesATrailingOne(t *testing.T) {
	store, err := session.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sess, _ := store.Create("/w", "m", "t")
	w := session.NewWriter(store, sess.ID)
	ctx := context.Background()
	_ = w.AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: "zbuduj mod"})
	for i := 0; i < 5; i++ {
		dropTrailingRewindMarker(ctx, store, sess.ID)
		_ = w.AppendMessage(ctx, llm.Message{Role: llm.RoleSystem, Content: rewindMarkerPrefix + " The user permanently removed the selected message."})
	}
	msgs, _ := store.ReadMessages(ctx, sess.ID)
	markers := 0
	for _, m := range msgs {
		if strings.HasPrefix(m.Content, rewindMarkerPrefix) {
			markers++
		}
	}
	if markers != 1 || len(msgs) != 2 {
		t.Fatalf("markers = %d, messages = %d", markers, len(msgs))
	}
}
