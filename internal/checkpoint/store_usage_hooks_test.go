package checkpoint

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"supercli/internal/tools"
)

func TestManagedUsageKnownCheckpointWritesRemainAboveMeasuredFiles(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	m := openSnapshotTestManager(t, home)
	for i, input := range []string{`{"path":"deep/dir/a.txt","content":"one"}`, `{"path":"deep/dir/b.txt","content":"two"}`, `{"path":"deep/dir/a.txt","content":"modified"}`} {
		turn := m.NewTurn("sid", "edit")
		runSnapshotTool(t, turn.Wrap(tools.NewWriteFile(home).Spec()), input)
		if _, err := turn.Complete(ctx); err != nil {
			t.Fatal(err)
		}
		unlock, err := m.lockStore(ctx)
		if err != nil {
			t.Fatal(err)
		}
		counter, err := m.usageCounterLocked()
		if err != nil {
			t.Fatal(err)
		}
		state, err := counter.readLocked()
		if err != nil {
			t.Fatal(err)
		}
		measured, measureErr := MeasureStoreRetentionLocked(ctx, filepath.Dir(m.gate.path))
		closeErr := unlock()
		if measureErr != nil || closeErr != nil {
			t.Fatalf("measure: %v %v", measureErr, closeErr)
		}
		if state.Dirty || state.Upper < measured.Bytes {
			t.Fatalf("turn %d counter undercount: %+v vs %+v", i, state, measured)
		}
	}
}

func TestManagedUsageReadOnlyTurnDoesNotInitializeLedger(t *testing.T) {
	m := openSnapshotTestManager(t, t.TempDir())
	turn := m.NewTurn("sid", "read-only chat")
	if record, err := turn.Complete(context.Background()); err != nil || record != nil {
		t.Fatalf("chat: %+v %v", record, err)
	}
	if _, err := os.Lstat(filepath.Join(filepath.Dir(m.gate.path), storeUsageName)); !os.IsNotExist(err) {
		t.Fatalf("chat initialized accounting: %v", err)
	}
	if m.usageCounter != nil {
		t.Fatal("chat retained counter")
	}
}
