package webgui

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"supercli/internal/checkpoint"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

func TestDeferredCheckpointChangesRecoverAfterRestart(t *testing.T) {
	ctx := context.Background()
	home, dataDir := t.TempDir(), t.TempDir()
	eng, err := NewEngine(echoConfig(), home, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	if err := os.WriteFile(filepath.Join(home, "a.txt"), []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(home, "echo", "synthetic deferred recovery")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, sess.ID)
	for _, msg := range []llm.Message{{Role: llm.RoleUser, Content: "synthetic edit"}, {Role: llm.RoleAssistant, Content: "worker accepted"}} {
		if err := writer.AppendMessage(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}
	receipt, err := store.ReadUserReceiptAt(ctx, sess.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := eng.checkpointManager(home)
	if err != nil {
		t.Fatal(err)
	}
	turn := manager.NewTurn(sess.ID, "synthetic edit")
	turn.SetUserMessageReceipt(receipt.Seq, receipt.ID)
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	tool := turn.Wrap(tools.Tool{Name: "write_file", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		close(entered)
		<-release
		return tools.Result{}, os.WriteFile(filepath.Join(home, "a.txt"), []byte("after"), 0600)
	}})
	toolDone := make(chan error, 1)
	go func() {
		result, err := tool.Fn(ctx, json.RawMessage("{\"path\":\"a.txt\"}"))
		toolDone <- errors.Join(err, result.Err)
	}()
	<-entered
	fixture, err := sql.Open("sqlite", filepath.Join(eng.DataDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer fixture.Close()
	if _, err := fixture.ExecContext(ctx, "CREATE TRIGGER synthetic_recovery_failure BEFORE UPDATE OF file_changes_json ON session_turns BEGIN SELECT RAISE(ABORT, 'synthetic transient failure'); END"); err != nil {
		t.Fatal(err)
	}
	sink := &deferredCheckpointChanges{store: store, sessionID: sess.ID, userSeq: receipt.Seq, userMessageID: receipt.ID, manager: manager}
	type completion struct {
		record *checkpoint.Record
		err    error
	}
	completed := make(chan completion, 1)
	immediate, err := turn.CompleteDeferred(ctx, func(record *checkpoint.Record, err error) { sink.publish(record); completed <- completion{record, err} })
	if err != nil || immediate != nil {
		t.Fatalf("worker did not defer: %+v %v", immediate, err)
	}
	binding := sink.bindCompletion(turn.CompletionKey())
	if binding == nil {
		t.Fatal("deferred binding was not prepared")
	}
	rowID, err := store.AppendTurnSummaryWithID(ctx, session.TurnSummary{CheckpointBinding: binding, SessionID: sess.ID, AssistantSeq: 2, Input: 17, Output: 5, ToolCalls: 1, DurationMS: 300})
	if err != nil {
		t.Fatal(err)
	}
	sink.bindSummary(2, rowID)
	unblock()
	if err := <-toolDone; err != nil {
		t.Fatal(err)
	}
	finished := <-completed
	if finished.err != nil || finished.record == nil || len(finished.record.Changes) != 1 || sink.flushed || sink.attempts != 1 {
		t.Fatalf("failure fixture did not retain durable changes: %+v attempts=%d", finished, sink.attempts)
	}
	if _, err := fixture.ExecContext(ctx, "DROP TRIGGER synthetic_recovery_failure"); err != nil {
		t.Fatal(err)
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewEngine(echoConfig(), home, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	messages, err := reopened.transcript(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	var final *transcriptTurn
	for _, m := range messages {
		if m.Seq == 2 {
			final = m.Turn
		}
	}
	if final == nil || len(final.FileChanges) != 1 || final.FileChanges[0].Path != "a.txt" || final.FileChanges[0].Kind != "modified" || final.TokIn != 17 || final.TokOut != 5 || final.ToolCalls != 1 || final.ElapsedMS != 300 {
		t.Fatalf("opening saved conversation did not recover original late changes and preserve telemetry: %+v", final)
	}
	reopenedStore, err := reopened.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := reopenedStore.ReadTurnSummaries(ctx, sess.ID)
	if err != nil || len(rows) != 1 || rows[0].CheckpointBinding == nil || !rows[0].CheckpointBinding.Resolved {
		t.Fatalf("repair not durable: %+v %v", rows, err)
	}
	// After success the persisted resolved flag bypasses checkpoint I/O. An
	// unreadable checkpoint store must not break this saved transcript.
	hash := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(home))))
	metadata := filepath.Join(reopened.DataDir(), "checkpoints", hex.EncodeToString(hash[:8]), "turns.json")
	if err := os.WriteFile(metadata, []byte("synthetic malformed metadata"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.transcript(ctx, sess.ID); err != nil {
		t.Fatalf("resolved summary reopened metadata: %v", err)
	}
}

func TestDeferredCheckpointBindingHandlesBothEventOrdersAndNoOp(t *testing.T) {
	for _, order := range []string{"record-before-binding", "record-first", "summary-first", "concurrent", "no-op", "failed-no-record"} {
		t.Run(order, func(t *testing.T) {
			ctx := context.Background()
			eng, err := NewEngine(echoConfig(), t.TempDir(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer eng.Close()
			store, err := eng.sessionStore()
			if err != nil {
				t.Fatal(err)
			}
			sess, err := store.Create(eng.Home(), "echo", "synthetic bound completion")
			if err != nil {
				t.Fatal(err)
			}
			writer := session.NewWriter(store, sess.ID)
			for _, msg := range []llm.Message{{Role: llm.RoleUser, Content: "request"}, {Role: llm.RoleAssistant, Content: "response"}} {
				if err := writer.AppendMessage(ctx, msg); err != nil {
					t.Fatal(err)
				}
			}
			receipt, err := store.ReadUserReceiptAt(ctx, sess.ID, 1)
			if err != nil {
				t.Fatal(err)
			}
			manager, err := eng.checkpointManager(eng.Home())
			if err != nil {
				t.Fatal(err)
			}
			sink := &deferredCheckpointChanges{store: store, sessionID: sess.ID, manager: manager}
			sink.setUserReceipt(receipt.Seq, receipt.ID)
			key := strings.Repeat("a", 32)
			record := &checkpoint.Record{ID: "original", CompletionKey: key, SessionID: sess.ID, UserSeq: 1, UserMessageID: receipt.ID,
				Changes: []checkpoint.FileChange{{Path: "correct.txt", Kind: "modified"}}}
			if order == "record-before-binding" {
				sink.complete(record, nil)
			}
			binding := sink.bindCompletion(key)
			if order == "record-first" {
				sink.complete(record, nil)
			}
			rowID, err := store.AppendTurnSummaryWithID(ctx, session.TurnSummary{SessionID: sess.ID, AssistantSeq: 2, Input: 17, CheckpointBinding: binding})
			if err != nil {
				t.Fatal(err)
			}
			if order == "concurrent" {
				var wg sync.WaitGroup
				wg.Add(2)
				go func() { defer wg.Done(); sink.bindSummary(2, rowID) }()
				go func() { defer wg.Done(); sink.complete(record, nil) }()
				wg.Wait()
			} else {
				sink.bindSummary(2, rowID)
				if order == "no-op" {
					sink.complete(nil, nil)
				} else if order == "failed-no-record" {
					sink.complete(nil, errors.New("synthetic failure"))
				} else {
					sink.complete(record, nil)
				}
			}
			rows, err := store.ReadTurnSummaries(ctx, sess.ID)
			if err != nil || len(rows) != 1 || rows[0].Input != 17 || rows[0].CheckpointBinding == nil {
				t.Fatalf("summary: %+v %v", rows, err)
			}
			wantResolved := order != "failed-no-record"
			wantChanges := 1
			if order == "no-op" || !wantResolved {
				wantChanges = 0
			}
			if rows[0].CheckpointBinding.Resolved != wantResolved || len(rows[0].FileChanges) != wantChanges {
				t.Fatalf("completion state: %+v", rows[0])
			}
		})
	}
}
