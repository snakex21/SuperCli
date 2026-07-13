package session

import (
	"context"
	"testing"

	"supercli/internal/llm"
)

func TestStoreTurnSummarySurvivesReopenAndClampsSubsets(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(dir, "model", "turns")
	if err != nil {
		t.Fatal(err)
	}
	writer := NewWriter(store, sess.ID)
	ctx := context.Background()
	if err := writer.AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := writer.AppendMessage(ctx, llm.Message{Role: llm.RoleAssistant, Content: "answer"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTurnSummary(ctx, TurnSummary{
		SessionID: sess.ID, DurationMS: 321, Input: 100, Output: 20,
		CachedInput: 999, Reasoning: 999, HasCachedInput: true,
		HasReasoning: true, ToolCalls: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	turns, err := store.ReadTurnSummaries(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 {
		t.Fatalf("turns = %+v", turns)
	}
	got := turns[0]
	if got.AssistantSeq != 2 || got.DurationMS != 321 || got.Input != 100 || got.Output != 20 || got.CachedInput != 100 || got.Reasoning != 20 || got.ToolCalls != 3 {
		t.Fatalf("turn summary = %+v", got)
	}
}

func TestStoreTurnSummaryRequiresAssistantMessage(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sess, err := store.Create(t.TempDir(), "model", "empty")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTurnSummary(context.Background(), TurnSummary{SessionID: sess.ID}); err == nil {
		t.Fatal("expected missing assistant error")
	}
}
