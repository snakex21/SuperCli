package session

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/system/stats"
)

func TestTurnWorkerContextJSONStorageRoundtrip(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	sess, err := store.Create(dir, "model", "worker counters")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	writer := NewWriter(store, sess.ID)
	if err := writer.AppendMessage(ctx, llm.Message{Role: llm.RoleAssistant, Content: "answer"}); err != nil {
		t.Fatal(err)
	}
	want := stats.WorkerContext{AddedMessages: 2, AddedBytes: 31, Requests: 3, MessageExposures: 6, ContextBytes: 93}
	summary := TurnSummary{SessionID: sess.ID, ToolDiag: TurnToolDiag{WorkerContext: &want, NoOpSearches: 1}}
	if summary.ToolDiag.Empty() {
		t.Fatal("context-only diagnostics empty")
	}
	if err := store.AppendTurnSummary(ctx, summary); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	turns, err := store.ReadTurnSummaries(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || turns[0].ToolDiag.WorkerContext == nil || *turns[0].ToolDiag.WorkerContext != want || turns[0].ToolDiag.NoOpSearches != 1 {
		t.Fatalf("roundtrip: %+v", turns)
	}
	recent, err := store.ReadRecentTurnTelemetry(ctx, time.Time{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 || recent[0].ToolDiag.WorkerContext == nil || *recent[0].ToolDiag.WorkerContext != want {
		t.Fatalf("projection: %+v", recent)
	}
	negative := stats.WorkerContext{AddedMessages: -1, AddedBytes: -2, Requests: -3, MessageExposures: -4, ContextBytes: -5}
	summary.ToolDiag = TurnToolDiag{WorkerContext: &negative, NoOpSearches: 2}
	if err := store.AppendTurnSummary(ctx, summary); err != nil {
		t.Fatal(err)
	}
	turns, err = store.ReadTurnSummaries(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if negative.Requests != -3 || turns[0].ToolDiag.WorkerContext != nil || turns[0].ToolDiag.NoOpSearches != 2 {
		t.Fatal("sanitization or upsert failed")
	}
	var old TurnToolDiag
	if err := json.Unmarshal([]byte(`{"noop_searches":1}`), &old); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(old)
	if err != nil || strings.Contains(string(b), "worker_context") {
		t.Fatalf("legacy JSON changed: %s %v", b, err)
	}
	if !(TurnToolDiag{WorkerContext: &stats.WorkerContext{}}).Empty() {
		t.Fatal("zero counters not empty")
	}
}
