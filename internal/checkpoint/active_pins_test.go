package checkpoint

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/tools"
)

func TestActivePinsRetainEarlierBaselineAcrossOtherCompletedTurn(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	writeCheckpointFixture(t, filepath.Join(home, "a.txt"), "a-original")
	writeCheckpointFixture(t, filepath.Join(home, "b.txt"), "b-original")
	m := openSnapshotTestManager(t, home)
	a := m.NewTurn("first", "edit a")
	b := m.NewTurn("second", "edit b")
	runSnapshotTool(t, a.Wrap(tools.NewWriteFile(home).Spec()), `{"path":"a.txt","content":"a-after"}`)
	runSnapshotTool(t, b.Wrap(tools.NewWriteFile(home).Spec()), `{"path":"b.txt","content":"b-after"}`)
	second, err := b.Complete(ctx)
	if err != nil || second == nil {
		t.Fatalf("second completion: %+v, %v", second, err)
	}
	if err := m.Clear(); !errors.Is(err, ErrActiveTurn) {
		t.Fatalf("Clear discarded active baseline: %v", err)
	}
	if _, err := m.Undo(ctx, second.ID); !errors.Is(err, ErrActiveTurn) {
		t.Fatalf("Undo ignored active workspace mutation: %v", err)
	}
	// This is an isolated fixture's private bare repository. A newer latest ref
	// and record must not permit maintenance to lose the earlier pending before.
	if _, err := m.git(ctx, "prune", "--expire=now"); err != nil {
		t.Fatal(err)
	}
	if got, err := m.git(ctx, "show", a.before+":a.txt"); err != nil || got != "a-original" {
		t.Fatalf("pending baseline lost: %q, %v", got, err)
	}
	first, err := a.Complete(ctx)
	if err != nil || first == nil {
		t.Fatalf("first completion: %+v, %v", first, err)
	}
	refs, err := m.git(ctx, "for-each-ref", "--format=%(refname)", "refs/supercli/active/")
	if err != nil || strings.TrimSpace(refs) != "" {
		t.Fatalf("successful completion leaked active roots: %q, %v", refs, err)
	}
	if _, err := m.Undo(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, filepath.Join(home, "a.txt"), []byte("a-original"))
	assertFileBytes(t, filepath.Join(home, "b.txt"), []byte("b-after"))
}

func TestNoopCompletionReleasesActivePinsAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	writeCheckpointFixture(t, filepath.Join(home, "same.txt"), "same")
	m := openSnapshotTestManager(t, home)
	turn := m.NewTurn("noop", "same bytes")
	runSnapshotTool(t, turn.Wrap(tools.NewWriteFile(home).Spec()), `{"path":"same.txt","content":"same"}`)
	for i := 0; i < 2; i++ {
		if record, err := turn.Complete(ctx); err != nil || record != nil {
			t.Fatalf("no-op completion %d: %+v, %v", i, record, err)
		}
	}
	refs, err := m.git(ctx, "for-each-ref", "--format=%(refname)", "refs/supercli/active/")
	if err != nil || strings.TrimSpace(refs) != "" || len(m.records) != 0 {
		t.Fatalf("no-op retained pins/records: refs=%q count=%d, %v", refs, len(m.records), err)
	}
	if err := m.Clear(); err != nil {
		t.Fatal(err)
	}
}

func TestFailedCompletionRetainsPinsUntilSafeRetry(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	writeCheckpointFixture(t, filepath.Join(home, "a.txt"), "before")
	m := openSnapshotTestManager(t, home)
	turn := m.NewTurn("retry", "edit")
	runSnapshotTool(t, turn.Wrap(tools.NewWriteFile(home).Spec()), `{"path":"a.txt","content":"after"}`)
	broken := []byte(`[{"id":"incomplete"},`)
	if err := os.WriteFile(m.meta, broken, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Complete(ctx); err == nil {
		t.Fatal("completion accepted damaged records")
	}
	refs, err := m.git(ctx, "for-each-ref", "--format=%(refname)", "refs/supercli/active/")
	if err != nil || !strings.Contains(refs, turn.active.root()+"/before") {
		t.Fatalf("failed completion released baseline: %q, %v", refs, err)
	}
	actual, err := os.ReadFile(m.meta)
	if err != nil || string(actual) != string(broken) {
		t.Fatal("damaged metadata was replaced")
	}
	if err := os.WriteFile(m.meta, []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := turn.Complete(ctx)
	if err != nil || first == nil {
		t.Fatalf("retry: %+v, %v", first, err)
	}
	second, err := turn.Complete(ctx)
	if err != nil || second == nil || first.ID != second.ID || len(m.records) != 1 {
		t.Fatalf("retry appended duplicate record: first=%+v second=%+v count=%d err=%v", first, second, len(m.records), err)
	}
	if _, err := m.Undo(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, filepath.Join(home, "a.txt"), []byte("before"))
}
