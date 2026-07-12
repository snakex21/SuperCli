package agent

import (
	"context"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

func TestHidePersistsModelProjectionWithoutChangingTranscript(t *testing.T) {
	store, err := session.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sess, err := store.Create(t.TempDir(), "echo", "")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, sess.ID)
	prov, _ := llm.NewEcho("echo")
	loop, err := NewLoop(LoopConfig{Provider: prov, Registry: tools.NewRegistry(), Writer: writer})
	if err != nil {
		t.Fatal(err)
	}
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "old one"},
		{Role: llm.RoleAssistant, Content: "old answer"},
		{Role: llm.RoleUser, Content: "current"},
	}
	loop.Messages = append([]llm.Message(nil), msgs...)
	for _, m := range msgs {
		if err := writer.AppendMessage(context.Background(), m); err != nil {
			t.Fatal(err)
		}
	}
	if got := loop.HideLastUserTurns(1); got != 2 {
		t.Fatalf("hidden = %d, want 2", got)
	}

	full, err := store.ReadMessages(context.Background(), sess.ID)
	if err != nil || len(full) != 3 {
		t.Fatalf("transcript = %d, %v", len(full), err)
	}
	model, err := store.ReadModelContext(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(model) != 2 || model[1].Content != "current" || model[0].Content == "old one" {
		t.Fatalf("model projection = %+v", model)
	}
}
