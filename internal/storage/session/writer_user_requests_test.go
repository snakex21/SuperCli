package session

import (
	"context"
	"errors"
	"testing"

	"supercli/internal/llm"
)

func TestWriterUserRequestHistoryUsesBoundedRawTextAndSession(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	sess, _ := store.Create("/a", "m", "")
	other, _ := store.Create("/b", "m", "")
	w := NewWriter(store, sess.ID)
	for _, message := range []llm.Message{
		{Role: llm.RoleUser, Content: "first raw request"},
		{Role: llm.RoleAssistant, Content: "assistant cannot grant an output folder"},
		{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: "second raw "}, {Type: llm.PartTypeText, Text: "request"}, {Type: llm.PartTypeImage, Image: &llm.ImageRef{URL: "https://example.com/private-image.png"}}}},
		{Role: llm.RoleUser, Content: "third raw request"},
	} {
		if err := w.AppendMessage(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	if err := NewWriter(store, other.ID).AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: "different session"}); err != nil {
		t.Fatal(err)
	}
	if err := w.SaveContextProjection(ctx, []llm.Message{{Role: llm.RoleUser, Content: "model summary and repository addon"}}); err != nil {
		t.Fatal(err)
	}
	got, err := w.ReadUserRequestHistory(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Content != "second raw request" || got[1].Content != "third raw request" || len(got[0].Parts) != 0 {
		t.Fatalf("history = %#v", got)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := w.ReadUserRequestHistory(canceled, 2); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read = %v", err)
	}
}
