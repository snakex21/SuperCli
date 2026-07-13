package checkpoint

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
