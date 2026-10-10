package checkpoint

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"supercli/internal/system/childproc"
)

func TestManagedUsageExistingReftableWritesRequireCensus(t *testing.T) {
	ctx := context.Background()
	m := openSnapshotTestManager(t, t.TempDir())
	cmd := exec.CommandContext(ctx, "git", "-c", "init.defaultRefFormat=reftable", "init", "--bare", "--object-format=sha1", m.repo)
	cmd.Env = checkpointGitEnv()
	childproc.HideWindow(cmd)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init existing backend: %v %s", err, output)
	}
	if _, err := os.Stat(filepath.Join(m.repo, "reftable")); os.IsNotExist(err) {
		t.Skip("Git does not support reftable")
	} else if err != nil {
		t.Fatal(err)
	}
	writeCheckpointFixture(t, filepath.Join(m.home, "app.txt"), "before")
	if _, err := m.capture(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.completeRetainedUsage(ctx, DefaultStoreBudgetBytes); err != nil {
		t.Fatal(err)
	}
	writeCheckpointFixture(t, filepath.Join(m.home, "app.txt"), "after")
	if _, err := m.capture(ctx); err != nil {
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
	state, readErr := counter.readLocked()
	closeErr := unlock()
	if readErr != nil || closeErr != nil || !state.Dirty {
		t.Fatalf("non-files backend published clean guess: %+v %v %v", state, readErr, closeErr)
	}
	if err := m.completeRetainedUsage(ctx, DefaultStoreBudgetBytes); err != nil {
		t.Fatal(err)
	}
	retentionTransaction(t, m, func() {
		state, err := counter.readLocked()
		if err != nil || state.Dirty {
			t.Fatalf("fresh census did not recover accounting: %+v %v", state, err)
		}
		physical, err := MeasureStoreRetentionLocked(ctx, filepath.Dir(m.gate.path))
		if err != nil || state.Upper < physical.Bytes {
			t.Fatalf("reftable undercount: %+v %+v %v", state, physical, err)
		}
	})
}
