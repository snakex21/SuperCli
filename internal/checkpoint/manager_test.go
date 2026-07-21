package checkpoint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"supercli/internal/tools"
)

func TestTurnUndoRedoAndConflictProtection(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	path := filepath.Join(home, "app.txt")
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Open(home, data)
	if errors.Is(err, ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	turn := m.NewTurn("session-1", "change app")
	spec := turn.Wrap(tools.NewWriteFile(home).Spec())
	res, _ := spec.Fn(context.Background(), json.RawMessage(`{"path":"app.txt","content":"after"}`))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	record, err := turn.Complete(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if record == nil || len(record.Files) != 1 || record.Files[0] != "app.txt" {
		t.Fatalf("record=%+v", record)
	}
	if len(record.Changes) != 1 || record.Changes[0] != (FileChange{Path: "app.txt", Kind: "modified"}) {
		t.Fatalf("changes=%+v", record.Changes)
	}
	controller := NewController(m, "session-1")
	preview, err := controller.Preview(false)
	if err != nil || preview.ID != record.ID || len(preview.Files) != 1 {
		t.Fatalf("undo preview=%+v err=%v", preview, err)
	}

	// A manual edit after the agent turn must never be overwritten.
	if err := os.WriteFile(path, []byte("manual"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := m.Undo(context.Background(), record.ID)
	if err == nil || len(result.Conflicts) != 1 {
		t.Fatalf("conflict result=%+v err=%v", result, err)
	}
	if got, _ := os.ReadFile(path); string(got) != "manual" {
		t.Fatalf("manual edit overwritten: %q", got)
	}

	if err := os.WriteFile(path, []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Undo(context.Background(), record.ID); err != nil {
		t.Fatal(err)
	}
	preview, err = controller.Preview(true)
	if err != nil || preview.ID != record.ID || !preview.Undone {
		t.Fatalf("redo preview=%+v err=%v", preview, err)
	}
	if got, _ := os.ReadFile(path); string(got) != "before" {
		t.Fatalf("undo=%q", got)
	}
	if _, err := m.Redo(context.Background(), record.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "after" {
		t.Fatalf("redo=%q", got)
	}

	// Metadata survives reopening the application.
	reopened, err := Open(home, data)
	if err != nil {
		t.Fatal(err)
	}
	if latest := reopened.Latest("session-1"); latest == nil || latest.ID != record.ID || latest.Undone {
		t.Fatalf("latest=%+v", latest)
	}
}

func TestUndoRemovesFileCreatedByTurn(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	m, err := Open(home, data)
	if errors.Is(err, ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	turn := m.NewTurn("s", "create")
	spec := turn.Wrap(tools.NewWriteFile(home).Spec())
	_, _ = spec.Fn(context.Background(), json.RawMessage(`{"path":"new.txt","content":"new"}`))
	record, err := turn.Complete(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Undo(context.Background(), record.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("created file remains: %v", err)
	}
}

func TestForgetFromDetachesDiscardedConversationCheckpoints(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	m, err := Open(home, data)
	if errors.Is(err, ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []Record{
		{ID: "keep", SessionID: "session", UserSeq: 1},
		{ID: "discard-a", SessionID: "session", UserSeq: 3},
		{ID: "discard-b", SessionID: "session", UserSeq: 5, Undone: true},
		{ID: "other", SessionID: "other-session", UserSeq: 7},
	} {
		if err := m.append(record); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.ForgetFrom("session", 3); err != nil {
		t.Fatal(err)
	}
	if got := m.PreviewFrom("session", 3); len(got.Records) != 0 {
		t.Fatalf("discarded checkpoints remain: %+v", got.Records)
	}
	if latest := m.Latest("session"); latest == nil || latest.ID != "keep" {
		t.Fatalf("latest checkpoint = %+v, want keep", latest)
	}
	if latest := m.Latest("other-session"); latest == nil || latest.ID != "other" {
		t.Fatalf("other session was changed: %+v", latest)
	}
	reopened, err := Open(home, data)
	if err != nil {
		t.Fatal(err)
	}
	if latest := reopened.Latest("session"); latest == nil || latest.ID != "keep" {
		t.Fatalf("persisted checkpoints = %+v", latest)
	}
}

func TestUndoRedoPreservesBinaryFiles(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	modifiedPath := filepath.Join(home, "image.png")
	createdPath := filepath.Join(home, "archive.zip")
	deletedPath := filepath.Join(home, "document.docx")
	modifiedBefore := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x00, 0xff, 0x80, 0x01}
	modifiedAfter := []byte{0x89, 'P', 'N', 'G', 0x00, 0xfe, 0x81, 0x02, 0x03}
	createdAfter := []byte{'P', 'K', 0x03, 0x04, 0x00, 0xff, 0x10}
	deletedBefore := []byte{'P', 'K', 0x03, 0x04, 0x00, 0x80, 0xfe, 0x7f}
	if err := os.WriteFile(modifiedPath, modifiedBefore, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deletedPath, deletedBefore, 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Open(home, data)
	if errors.Is(err, ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	turn := m.NewTurn("binary-session", "change binary files")
	if err := turn.ensureBefore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modifiedPath, modifiedAfter, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(createdPath, createdAfter, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(deletedPath); err != nil {
		t.Fatal(err)
	}
	record, err := turn.Complete(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if record == nil || len(record.Files) != 3 {
		t.Fatalf("record=%+v", record)
	}
	wantChanges := []FileChange{
		{Path: "archive.zip", Kind: "created"},
		{Path: "document.docx", Kind: "deleted"},
		{Path: "image.png", Kind: "modified"},
	}
	if !reflect.DeepEqual(record.Changes, wantChanges) {
		t.Fatalf("changes=%+v, want %+v", record.Changes, wantChanges)
	}

	if _, err := m.Undo(context.Background(), record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, modifiedPath, modifiedBefore)
	assertFileBytes(t, deletedPath, deletedBefore)
	if _, err := os.Stat(createdPath); !os.IsNotExist(err) {
		t.Fatalf("created binary file remains after undo: %v", err)
	}

	if _, err := m.Redo(context.Background(), record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, modifiedPath, modifiedAfter)
	assertFileBytes(t, createdPath, createdAfter)
	if _, err := os.Stat(deletedPath); !os.IsNotExist(err) {
		t.Fatalf("deleted binary file returned after redo: %v", err)
	}
}

func assertFileBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s bytes = %x, want %x", filepath.Base(path), got, want)
	}
}

func TestReadOnlyTurnCreatesNoCheckpoint(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	m, err := Open(home, data)
	if errors.Is(err, ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	turn := m.NewTurn("s", "read")
	if record, err := turn.Complete(context.Background()); err != nil || record != nil {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	if _, err := os.Stat(m.repo); !os.IsNotExist(err) {
		t.Fatalf("read-only turn initialized shadow git: %v", err)
	}
}

func TestUndoFromRewindsMultipleTurnsAndCanRollback(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	path := filepath.Join(home, "app.txt")
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Open(home, data)
	if errors.Is(err, ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	writeTurn := func(seq int, value string) {
		t.Helper()
		turn := m.NewTurn("session", value)
		turn.SetUserSeq(seq)
		spec := turn.Wrap(tools.NewWriteFile(home).Spec())
		result, _ := spec.Fn(context.Background(), json.RawMessage(`{"path":"app.txt","content":"`+value+`"}`))
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		if record, completeErr := turn.Complete(context.Background()); completeErr != nil || record == nil {
			t.Fatalf("complete seq %d: record=%+v err=%v", seq, record, completeErr)
		}
	}
	writeTurn(1, "one")
	writeTurn(3, "two")
	preview := m.PreviewFrom("session", 1)
	if len(preview.Records) != 2 || len(preview.Files) != 1 || preview.Files[0] != "app.txt" {
		t.Fatalf("preview=%+v", preview)
	}
	batch, err := m.UndoFrom(context.Background(), "session", 1)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "before" {
		t.Fatalf("multi-undo=%q", got)
	}
	if err := m.RedoBatch(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "two" {
		t.Fatalf("batch rollback=%q", got)
	}
}

func TestUndoFromConflictRollsBackAlreadyAppliedCheckpoints(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	firstPath, secondPath := filepath.Join(home, "first.txt"), filepath.Join(home, "second.txt")
	if err := os.WriteFile(firstPath, []byte("first-before"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, []byte("second-before"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Open(home, data)
	if errors.Is(err, ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	write := func(seq int, path, value string) {
		t.Helper()
		turn := m.NewTurn("session", value)
		turn.SetUserSeq(seq)
		spec := turn.Wrap(tools.NewWriteFile(home).Spec())
		result, _ := spec.Fn(context.Background(), json.RawMessage(`{"path":"`+path+`","content":"`+value+`"}`))
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		if record, completeErr := turn.Complete(context.Background()); completeErr != nil || record == nil {
			t.Fatalf("record=%+v err=%v", record, completeErr)
		}
	}
	write(1, "first.txt", "first-after")
	write(3, "second.txt", "second-after")
	if err := os.WriteFile(firstPath, []byte("manual"), 0o644); err != nil {
		t.Fatal(err)
	}
	batch, err := m.UndoFrom(context.Background(), "session", 1)
	if err == nil || len(batch.Conflicts) != 1 || batch.Conflicts[0] != "first.txt" {
		t.Fatalf("batch=%+v err=%v", batch, err)
	}
	if got, _ := os.ReadFile(firstPath); string(got) != "manual" {
		t.Fatalf("manual conflict overwritten: %q", got)
	}
	if got, _ := os.ReadFile(secondPath); string(got) != "second-after" {
		t.Fatalf("later checkpoint was not rolled forward: %q", got)
	}
}

func TestRedoIDsIsExactAndRollsBackPartialRestore(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	firstPath := filepath.Join(home, "first.bin")
	secondPath := filepath.Join(home, "second.bin")
	if err := os.WriteFile(firstPath, []byte{0x00, 0x01}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, []byte{0x10, 0x11}, 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Open(home, data)
	if errors.Is(err, ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	writeTurn := func(seq int, path string, value []byte) {
		t.Helper()
		turn := m.NewTurn("redo-session", "binary change")
		turn.SetUserSeq(seq)
		if err := turn.ensureBefore(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, value, 0o644); err != nil {
			t.Fatal(err)
		}
		if record, completeErr := turn.Complete(context.Background()); completeErr != nil || record == nil {
			t.Fatalf("record=%+v err=%v", record, completeErr)
		}
	}
	writeTurn(1, firstPath, []byte{0x02, 0x03})
	writeTurn(3, secondPath, []byte{0x12, 0x13})
	undone, err := m.UndoFrom(context.Background(), "redo-session", 1)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(undone.Records))
	for _, record := range undone.Records {
		ids = append(ids, record.ID)
	}

	// The oldest redo applies first. Force the newer redo to conflict and
	// verify that the already-applied older redo is rolled back.
	if err := os.WriteFile(secondPath, []byte{0xff, 0xfe}, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := m.RedoIDs(context.Background(), "redo-session", ids)
	if err == nil || len(result.Conflicts) != 1 || result.Conflicts[0] != "second.bin" {
		t.Fatalf("redo conflict result=%+v err=%v", result, err)
	}
	assertFileBytes(t, firstPath, []byte{0x00, 0x01})
	assertFileBytes(t, secondPath, []byte{0xff, 0xfe})

	if err := os.WriteFile(secondPath, []byte{0x10, 0x11}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RedoIDs(context.Background(), "redo-session", ids); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, firstPath, []byte{0x02, 0x03})
	assertFileBytes(t, secondPath, []byte{0x12, 0x13})
}

func TestControllerCompletesOldestTurnWhenNextStartsQuickly(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	m, err := Open(home, data)
	if errors.Is(err, ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	c := NewController(m, "session")
	spec := c.Wrap(tools.NewWriteFile(home).Spec())
	c.Start("first")
	_, _ = spec.Fn(context.Background(), json.RawMessage(`{"path":"first.txt","content":"1"}`))
	// TUI dispatches OnRunEnd asynchronously; a very fast next submit may
	// call Start before the prior completion goroutine runs.
	c.Start("second")
	first, err := c.Complete(context.Background())
	if err != nil || first == nil || first.Prompt != "first" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	_, _ = spec.Fn(context.Background(), json.RawMessage(`{"path":"second.txt","content":"2"}`))
	second, err := c.Complete(context.Background())
	if err != nil || second == nil || second.Prompt != "second" {
		t.Fatalf("second=%+v err=%v", second, err)
	}
}
