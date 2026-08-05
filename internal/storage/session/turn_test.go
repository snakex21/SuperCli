package session

import (
	"context"
	"testing"
	"time"

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
		HasReasoning: true, ToolCalls: 3, ToolFailures: 1, Steps: 2,
		ModelCalls: 3, FailedCalls: 1, CanceledCalls: 1, BackgroundCalls: 1,
		HelperCalls: 2, AuxCalls: 2, AuxUs: 50_000,
		Phases: map[string]int64{"backend_wait": 1234, "tool:read": 456},
		FileChanges: []FileChange{{Path: "new.txt", Kind: "created"}},
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
	if got.ToolFailures != 1 || got.Steps != 2 || got.ModelCalls != 3 || got.FailedCalls != 1 || got.CanceledCalls != 1 || got.BackgroundCalls != 1 || got.HelperCalls != 2 || got.AuxCalls != 2 || got.AuxUs != 50_000 || got.Phases["backend_wait"] != 1234 {
		t.Fatalf("turn telemetry = %+v", got)
	}
	if len(got.FileChanges) != 1 || got.FileChanges[0] != (FileChange{Path: "new.txt", Kind: "created"}) {
		t.Fatalf("turn file changes = %+v", got.FileChanges)
	}
}

func TestStoreRecentTurnSummariesIsBoundedAcrossSessions(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		sess, createErr := store.Create(t.TempDir(), "model", "recent")
		if createErr != nil {
			t.Fatal(createErr)
		}
		writer := NewWriter(store, sess.ID)
		if err := writer.AppendMessage(ctx, llm.Message{Role: llm.RoleAssistant, Content: "ok"}); err != nil {
			t.Fatal(err)
		}
		if err := store.AppendTurnSummary(ctx, TurnSummary{SessionID: sess.ID, Phases: map[string]int64{"backend_wait": int64(i + 1)}, CreatedAt: time.Now().Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.ReadRecentTurnSummaries(ctx, time.Now().Add(-time.Hour), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Phases["backend_wait"] != 3 {
		t.Fatalf("recent summaries = %+v", got)
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
