package session

import (
	"context"
	"path/filepath"
	"testing"

	"supercli/internal/llm"
)

func TestMessageAttachmentsPersistAndFollowRewind(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sess, err := store.Create(t.TempDir(), "echo", "attachments")
	if err != nil {
		t.Fatal(err)
	}
	writer := NewWriter(store, sess.ID)
	for _, content := range []string{"first", "assistant", "second"} {
		role := llm.RoleUser
		if content == "assistant" {
			role = llm.RoleAssistant
		}
		if err := writer.AppendMessage(ctx, llm.Message{Role: role, Content: content}); err != nil {
			t.Fatal(err)
		}
	}
	first := filepath.Join(t.TempDir(), "first.png")
	second := filepath.Join(t.TempDir(), "second.jpg")
	if err := store.SaveMessageAttachments(ctx, sess.ID, 1, []string{first}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMessageAttachments(ctx, sess.ID, 3, []string{second}); err != nil {
		t.Fatal(err)
	}
	got, err := store.ReadMessageAttachmentsRange(ctx, sess.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || len(got[1]) != 1 || got[1][0] != first || got[3][0] != second {
		t.Fatalf("attachments = %#v", got)
	}
	if _, err := store.TruncateFrom(ctx, sess.ID, 3); err != nil {
		t.Fatal(err)
	}
	got, err = store.ReadMessageAttachmentsRange(ctx, sess.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[1][0] != first {
		t.Fatalf("attachments after rewind = %#v", got)
	}
}
