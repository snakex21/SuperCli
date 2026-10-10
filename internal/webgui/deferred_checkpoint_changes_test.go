package webgui

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"supercli/internal/checkpoint"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"sync"
	"testing"
)

func TestDeferredCheckpointChangesPreserveExactResponse(t *testing.T) {
	for _, order := range []string{"record-first", "summary-first", "concurrent"} {
		t.Run(order, func(t *testing.T) {
			ctx := context.Background()
			eng, err := NewEngine(echoConfig(), t.TempDir(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = eng.Close() })
			store, err := eng.sessionStore()
			if err != nil {
				t.Fatal(err)
			}
			sess, err := store.Create(eng.Home(), "echo", "synthetic")
			if err != nil {
				t.Fatal(err)
			}
			writer := session.NewWriter(store, sess.ID)
			appendPair := func() {
				t.Helper()
				if err := writer.AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: "synthetic request"}); err != nil {
					t.Fatal(err)
				}
				if err := writer.AppendMessage(ctx, llm.Message{Role: llm.RoleAssistant, Content: "synthetic reply"}); err != nil {
					t.Fatal(err)
				}
			}
			appendPair()
			first := session.TurnSummary{SessionID: sess.ID, AssistantSeq: 2, Input: 17, Output: 5, DurationMS: 300, ToolCalls: 1}
			var firstID int64
			persist := func() {
				t.Helper()
				firstID, err = store.AppendTurnSummaryWithID(ctx, first)
				if err != nil || firstID <= 0 {
					t.Fatalf("summary receipt: %d %v", firstID, err)
				}
			}
			sink := &deferredCheckpointChanges{store: store, sessionID: sess.ID, userSeq: 1}
			record := &checkpoint.Record{SessionID: sess.ID, UserSeq: 1, Changes: []checkpoint.FileChange{{Path: "a.txt", Kind: "created"}}}
			switch order {
			case "record-first":
				sink.publish(record)
				persist()
				sink.bindSummary(2, firstID)
			case "summary-first":
				persist()
				sink.bindSummary(2, firstID)
				sink.publish(record)
			case "concurrent":
				persist()
				var wg sync.WaitGroup
				wg.Add(2)
				go func() { defer wg.Done(); sink.bindSummary(2, firstID) }()
				go func() { defer wg.Done(); sink.publish(record) }()
				wg.Wait()
			}
			appendPair()
			secondID, err := store.AppendTurnSummaryWithID(ctx, session.TurnSummary{SessionID: sess.ID, AssistantSeq: 4, Input: 23, Output: 7})
			if err != nil {
				t.Fatal(err)
			}
			sink.bindSummary(4, secondID)
			sink.publish(&checkpoint.Record{SessionID: sess.ID, UserSeq: 3, Changes: []checkpoint.FileChange{{Path: "wrong.txt", Kind: "deleted"}}})
			turns, err := store.ReadTurnSummaries(ctx, sess.ID)
			if err != nil || len(turns) != 2 {
				t.Fatalf("summaries: %+v %v", turns, err)
			}
			if len(turns[0].FileChanges) != 1 || turns[0].FileChanges[0].Path != "a.txt" || turns[0].Input != 17 || turns[0].DurationMS != 300 || turns[0].ToolCalls != 1 || len(turns[1].FileChanges) != 0 || turns[1].Input != 23 {
				t.Fatalf("late changes reassigned or replaced telemetry: %+v", turns)
			}
			stale := &deferredCheckpointChanges{store: store, sessionID: sess.ID, userSeq: 1}
			stale.bindSummary(2, firstID)
			if _, err := store.TruncateFrom(ctx, sess.ID, 1); err != nil {
				t.Fatal(err)
			}
			appendPair()
			reusedID, err := store.AppendTurnSummaryWithID(ctx, session.TurnSummary{SessionID: sess.ID, AssistantSeq: 2, Input: 31})
			if err != nil {
				t.Fatal(err)
			}
			if reusedID == firstID {
				t.Fatal("rewind reused a physical summary identity")
			}
			stale.publish(record)
			if err := store.UpdateTurnFileChanges(ctx, sess.ID, 2, firstID, []session.FileChange{{Path: "ghost.txt", Kind: "created"}}); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("late update admitted reused response: %v", err)
			}
			turns, err = store.ReadTurnSummaries(ctx, sess.ID)
			if err != nil || len(turns) != 1 || turns[0].Input != 31 || len(turns[0].FileChanges) != 0 {
				t.Fatalf("stale worker corrupted rewound response: %+v %v", turns, err)
			}
		})
	}
}
func TestDeferredCheckpointChangesRejectWrongOwner(t *testing.T) {
	eng, err := NewEngine(echoConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	store, err := eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(eng.Home(), "echo", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	sink := &deferredCheckpointChanges{store: store, sessionID: sess.ID, userSeq: 7}
	sink.publish(&checkpoint.Record{SessionID: "other", UserSeq: 7})
	sink.publish(&checkpoint.Record{SessionID: sess.ID, UserSeq: 8})
	if sink.published {
		t.Fatal("wrong owner admitted")
	}
	// Failed telemetry writes keep their receipt/changes for a later explicit
	// event. The actual checkpoint remains in its own durable records either way.
	record := &checkpoint.Record{SessionID: sess.ID, UserSeq: 7, Changes: []checkpoint.FileChange{{Path: "kept.txt", Kind: "created"}}}
	sink.bindSummary(2, 999)
	sink.publish(record)
	if !sink.flushed || len(sink.changes) != 0 {
		t.Fatal("stale response retained pointless retries")
	}
}

func TestDeferredCheckpointChangesRetryOneTransientFailure(t *testing.T) {
	ctx := context.Background()
	eng, err := NewEngine(echoConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	store, err := eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(eng.Home(), "echo", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, sess.ID)
	for _, msg := range []llm.Message{{Role: llm.RoleUser, Content: "request"}, {Role: llm.RoleAssistant, Content: "reply"}} {
		if err := writer.AppendMessage(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}
	rowID, err := store.AppendTurnSummaryWithID(ctx, session.TurnSummary{SessionID: sess.ID, AssistantSeq: 2})
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := sql.Open("sqlite", filepath.Join(eng.DataDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer fixture.Close()
	if _, err := fixture.ExecContext(ctx, "CREATE TRIGGER synthetic_late_update_failure BEFORE UPDATE OF file_changes_json ON session_turns BEGIN SELECT RAISE(ABORT, 'synthetic transient failure'); END"); err != nil {
		t.Fatal(err)
	}
	sink := &deferredCheckpointChanges{store: store, sessionID: sess.ID, userSeq: 1}
	record := &checkpoint.Record{ID: "original", SessionID: sess.ID, UserSeq: 1, Changes: []checkpoint.FileChange{{Path: "correct.txt", Kind: "created"}}}
	sink.bindSummary(2, rowID)
	sink.publish(record)
	if sink.flushed || len(sink.changes) != 1 || sink.attempts != 1 {
		t.Fatal("transient failure discarded original changes")
	}
	if _, err := fixture.ExecContext(ctx, "DROP TRIGGER synthetic_late_update_failure"); err != nil {
		t.Fatal(err)
	}
	sink.publish(&checkpoint.Record{ID: "wrong", SessionID: sess.ID, UserSeq: 1})
	if sink.attempts != 1 {
		t.Fatal("different record triggered a retry")
	}
	sink.bindSummary(2, rowID)
	if !sink.flushed || sink.attempts != 2 || len(sink.changes) != 0 {
		t.Fatal("same response retry did not finish")
	}
	sink.publish(record)
	sink.bindSummary(2, rowID)
	if sink.attempts != 2 {
		t.Fatal("completed sink repeated SQL")
	}
	turns, err := store.ReadTurnSummaries(ctx, sess.ID)
	if err != nil || len(turns) != 1 || len(turns[0].FileChanges) != 1 || turns[0].FileChanges[0].Path != "correct.txt" {
		t.Fatalf("retry results: %+v %v", turns, err)
	}
}
