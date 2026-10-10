package checkpoint

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"supercli/internal/tools"
)

func TestUserReceiptLateIndependentManagerDoesNotBindReusedSequence(t *testing.T) {
	ctx := context.Background()
	home, data := t.TempDir(), t.TempDir()
	path := filepath.Join(home, "a.txt")
	writeCheckpointFixture(t, path, "before")
	oldManager, err := Open(home, data)
	if err != nil {
		t.Fatal(err)
	}
	currentManager, err := Open(home, data)
	if err != nil {
		t.Fatal(err)
	}
	currentID := int64(101)
	validate := func(_ context.Context, sid string, seq int, id int64) (bool, error) {
		return sid == "sid" && seq == 7 && id == currentID, nil
	}
	oldManager.SetUserReceiptValidator(validate)
	currentManager.SetUserReceiptValidator(validate)
	oldTurn := oldManager.NewTurn("sid", "old accepted work")
	oldTurn.SetUserMessageReceipt(7, 101)
	runSnapshotTool(t, oldTurn.Wrap(tools.NewWriteFile(home).Spec()), `{"path":"a.txt","content":"old worker result"}`)
	// No record exists yet. A transcript-only rewind must still detach its
	// original row; a separate manager cannot detach oldTurn's in-memory seq.
	if _, err := currentManager.RewindTranscript(ctx, "sid", 7, 101, false, func(context.Context) error { currentID = 0; return nil }); err != nil {
		t.Fatal(err)
	}
	currentID = 201 // The replacement prompt reuses seq=7, never id=101.
	record, err := oldTurn.Complete(ctx)
	if err != nil || record == nil || record.UserMessageID != 101 {
		t.Fatalf("late record: %+v %v", record, err)
	}
	preview, err := currentManager.PreviewFromContext(ctx, "sid", 7)
	if err != nil || len(preview.Records) != 0 {
		t.Fatalf("ABA preview: %+v %v", preview, err)
	}
	if batch, err := currentManager.UndoFrom(ctx, "sid", 7); err != nil || len(batch.Records) != 0 {
		t.Fatalf("ABA undo: %+v %v", batch, err)
	}
	assertFileBytes(t, path, []byte("old worker result"))
	called := false
	if _, err := currentManager.RewindTranscript(ctx, "sid", 7, 101, true, func(context.Context) error { called = true; return nil }); !errors.Is(err, ErrUserMessageChanged) || called {
		t.Fatalf("stale selection: %v callback=%v", err, called)
	}
	// Explicit recovery by immutable checkpoint ID is still possible.
	if _, err := currentManager.Undo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, path, []byte("before"))
}

func TestUserReceiptLegacyRecordsExcludedFromAutomaticRewind(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	m := openSnapshotTestManager(t, home)
	turn := m.NewTurn("sid", "legacy")
	turn.SetUserSeq(1)
	runSnapshotTool(t, turn.Wrap(tools.NewWriteFile(home).Spec()), `{"path":"a.txt","content":"after"}`)
	record, err := turn.Complete(ctx)
	if err != nil || record == nil {
		t.Fatalf("legacy: %+v %v", record, err)
	}
	calls := 0
	m.SetUserReceiptValidator(func(context.Context, string, int, int64) (bool, error) { calls++; return true, nil })
	preview, err := m.PreviewFromContext(ctx, "sid", 1)
	if err != nil || len(preview.Records) != 0 || calls != 0 {
		t.Fatalf("legacy guessed: %+v %v calls=%d", preview, err, calls)
	}
	if _, err := m.Undo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
}

func TestUserReceiptFailedTruncateRedoesExactBatch(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	path := filepath.Join(home, "a.txt")
	writeCheckpointFixture(t, path, "before")
	m := openSnapshotTestManager(t, home)
	m.SetUserReceiptValidator(func(_ context.Context, sid string, seq int, id int64) (bool, error) {
		return sid == "sid" && seq == 1 && id == 11, nil
	})
	turn := m.NewTurn("sid", "edit")
	turn.SetUserMessageReceipt(1, 11)
	runSnapshotTool(t, turn.Wrap(tools.NewWriteFile(home).Spec()), `{"path":"a.txt","content":"after"}`)
	record, err := turn.Complete(ctx)
	if err != nil || record == nil {
		t.Fatalf("complete: %+v %v", record, err)
	}
	sentinel := errors.New("synthetic SQL commit failure")
	_, err = m.RewindTranscript(ctx, "sid", 1, 11, true, func(context.Context) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("truncate: %v", err)
	}
	assertFileBytes(t, path, []byte("after"))
	if got := m.Latest("sid"); got == nil || got.Undone {
		t.Fatalf("failed truncation left undone record: %+v", got)
	}
}

func TestCompletionRetryKeepsAlreadyCapturedAfterSnapshot(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	path := filepath.Join(home, "a.txt")
	writeCheckpointFixture(t, path, "before")
	m := openSnapshotTestManager(t, home)
	turn := m.NewTurn("sid", "edit")
	runSnapshotTool(t, turn.Wrap(tools.NewWriteFile(home).Spec()), `{"path":"a.txt","content":"agent after"}`)
	blocker := m.meta + ".tmp"
	if err := os.Mkdir(blocker, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocker, "keep"), []byte("prevent deferred empty-dir removal"), 0600); err != nil {
		t.Fatal(err)
	}
	if record, err := turn.Complete(ctx); err == nil || record != nil {
		t.Fatalf("expected admission failure: %+v %v", record, err)
	}
	originalAfter := turn.snapshotAfter
	if originalAfter == "" {
		t.Fatal("known after capture was lost")
	}
	if err := os.Remove(filepath.Join(blocker, "keep")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	writeCheckpointFixture(t, path, "later manual edit")
	record, err := turn.Complete(ctx)
	if err != nil || record == nil {
		t.Fatalf("retry: %+v %v", record, err)
	}
	if turn.snapshotAfter != originalAfter {
		t.Fatal("retry recaptured later manual edits")
	}
	if _, err := m.Undo(ctx, record.ID); err == nil {
		t.Fatal("later manual edit attributed to failed turn")
	}
	assertFileBytes(t, path, []byte("later manual edit"))
	writeCheckpointFixture(t, path, "agent after")
	if _, err := m.Undo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, path, []byte("before"))
}
