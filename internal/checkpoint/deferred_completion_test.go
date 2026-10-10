package checkpoint

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"supercli/internal/tools"
)

func TestDeferredCompletionWaitsForActualFnWithoutBlockingForeground(t *testing.T) {
	home := t.TempDir()
	writeCheckpointFixture(t, filepath.Join(home, "a.txt"), "before")
	m := openSnapshotTestManager(t, home)
	turn := m.NewTurn("original-session", "background edit")
	turn.SetUserSeq(7)
	borrow, err := BorrowInvocation(WithTurn(context.Background(), turn, &turn.barrier), true)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	tool := turn.Wrap(tools.Tool{Name: "write_file", Fn: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return tools.Result{}, ctx.Err()
		}
		return tools.Result{}, os.WriteFile(filepath.Join(home, "a.txt"), []byte("after"), 0600)
	}})
	toolDone := make(chan error, 1)
	go func() {
		defer borrow.Close()
		result, err := tool.Fn(borrow.Bind(context.Background()), json.RawMessage(`{"path":"a.txt"}`))
		toolDone <- errors.Join(err, result.Err)
	}()
	<-entered
	type completed struct {
		record *Record
		err    error
	}
	done := make(chan completed, 2)
	ctx, cancel := context.WithCancel(context.Background())
	foreground := make(chan completed, 1)
	go func() {
		record, err := turn.CompleteDeferred(ctx, func(record *Record, err error) {
			done <- completed{record, err}
		})
		foreground <- completed{record, err}
	}()
	select {
	case result := <-foreground:
		if result.err != nil || result.record != nil {
			t.Fatalf("pending turn returned a premature record/error: %+v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("foreground waited for background Fn")
	}
	cancel() // Closing the UI cannot cancel the event-driven finalizer.
	if _, err := turn.CompleteDeferred(context.Background(), func(_ *Record, _ error) {
		t.Error("a second completion waiter was scheduled")
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		t.Fatalf("checkpoint captured while actual Fn was blocked: %+v", result)
	default:
	}
	unblock()
	if err := <-toolDone; err != nil {
		t.Fatal(err)
	}
	result := <-done
	if result.err != nil || result.record == nil || result.record.SessionID != "original-session" || result.record.UserSeq != 7 {
		t.Fatalf("deferred completion: %+v", result)
	}
	if _, err := m.Undo(context.Background(), result.record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, filepath.Join(home, "a.txt"), []byte("before"))
	if _, err := m.Redo(context.Background(), result.record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, filepath.Join(home, "a.txt"), []byte("after"))
}

func TestDeferredCompletionKeepsNormalTurnInline(t *testing.T) {
	home := t.TempDir()
	m := openSnapshotTestManager(t, home)
	turn := m.NewTurn("normal", "edit")
	runSnapshotTool(t, turn.Wrap(tools.NewWriteFile(home).Spec()), `{"path":"a.txt","content":"after"}`)
	record, err := turn.CompleteDeferred(context.Background(), func(_ *Record, _ error) {
		t.Error("normal turn unnecessarily used a background finalizer")
	})
	if err != nil || record == nil || len(m.records) != 1 {
		t.Fatalf("normal completion: %+v, %v", record, err)
	}
}
