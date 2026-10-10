package checkpoint

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"supercli/internal/tools"
	"testing"
)

func pendingOwnerCount(t *testing.T, m *Manager) int {
	t.Helper()
	m.pendingMu.Lock()
	defer m.pendingMu.Unlock()
	return len(m.pending)
}
func pendingWrite(t *testing.T, tool tools.Tool, content string) {
	t.Helper()
	args, err := json.Marshal(map[string]string{"path": "a.txt", "content": content})
	if err != nil {
		t.Fatal(err)
	}
	runSnapshotTool(t, tool, string(args))
}
func TestPendingOwnerSurvivesControllerPopWithoutRecapturing(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	m := openSnapshotTestManager(t, home)
	writeCheckpointFixture(t, filepath.Join(home, "a.txt"), "before")
	c := NewController(m, "synthetic-pending")
	c.Start("edit a")
	turn := c.currentTurn()
	pendingWrite(t, c.Wrap(tools.NewWriteFile(home).Spec()), "after")
	if pendingOwnerCount(t, m) != 1 {
		t.Fatal("admitted turn has no manager owner")
	}
	if err := os.WriteFile(m.meta, []byte("[broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Complete(ctx); err == nil {
		t.Fatal("completion accepted malformed metadata")
	}
	if c.currentTurn() != nil || pendingOwnerCount(t, m) != 1 {
		t.Fatal("controller pop lost the failed checkpoint owner")
	}
	if err := os.WriteFile(m.meta, []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	writeCheckpointFixture(t, filepath.Join(home, "a.txt"), "later manual edit")
	if err := m.RetryRecordedCompletions(ctx); err != nil {
		t.Fatal(err)
	}
	if len(m.records) != 0 || pendingOwnerCount(t, m) != 1 {
		t.Fatal("unrecorded retry captured later edits or dropped the baseline")
	}
	assertFileBytes(t, filepath.Join(home, "a.txt"), []byte("later manual edit"))
	if _, err := turn.manager.git(ctx, "show", turn.before+":a.txt"); err != nil {
		t.Fatal(err)
	}
	// Release only this disposable fixture; production never guesses its after.
	if err := turn.releaseActivePins(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestRecordedCleanupRetryPreservesCheckpointAndManualEdits(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	m := openSnapshotTestManager(t, home)
	writeCheckpointFixture(t, filepath.Join(home, "a.txt"), "original\r\n")
	turn := m.NewTurn("synthetic-recorded", "edit a")
	turn.SetUserSeq(7)
	pendingWrite(t, turn.Wrap(tools.NewWriteFile(home).Spec()), "after\r\n")
	var metadata []byte
	err := turn.barrier.Complete(ctx, func(ctx context.Context) (bool, error) {
		committed, err := turn.commitCheckpoint(ctx)
		if err != nil || !committed {
			return committed, err
		}
		metadata, err = os.ReadFile(m.meta)
		if err != nil {
			return true, err
		}
		return true, os.WriteFile(m.meta, []byte("[broken"), 0600)
	}, turn.releaseActivePins)
	if err == nil || !turn.barrier.recordedCompletionReady() || pendingOwnerCount(t, m) != 1 {
		t.Fatalf("cleanup failure lost recorded recovery: %v", err)
	}
	if err := os.WriteFile(m.meta, metadata, 0600); err != nil {
		t.Fatal(err)
	}
	writeCheckpointFixture(t, filepath.Join(home, "manual.txt"), "later manual work")
	record := turn.completed
	if record == nil {
		t.Fatal("successful append was not retained")
	}
	if _, err := m.Undo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	if len(m.records) != 1 || m.records[0].ID != record.ID || m.records[0].UserSeq != 7 || pendingOwnerCount(t, m) != 0 {
		t.Fatal("cleanup retry duplicated or reassigned checkpoint")
	}
	assertFileBytes(t, filepath.Join(home, "a.txt"), []byte("original\r\n"))
	assertFileBytes(t, filepath.Join(home, "manual.txt"), []byte("later manual work"))
	if err := m.Clear(); err != nil {
		t.Fatal(err)
	}
}
