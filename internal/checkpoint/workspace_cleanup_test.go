package checkpoint

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"supercli/internal/tools"
)

func cleanupFixture(t *testing.T) (*Manager, string) {
	t.Helper()
	home, data := t.TempDir(), t.TempDir()
	m, err := Open(home, data)
	if errors.Is(err, ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	return m, data
}

func TestWorkspaceCleanupExactStoresPreservesChatsAndProjectFiles(t *testing.T) {
	m, data := cleanupFixture(t)
	ctx := context.Background()
	writeCheckpointFixture(t, filepath.Join(m.home, "keep.txt"), "before")
	turn := m.NewTurn("shared-conversation-one", "edit")
	runSnapshotTool(t, turn.Wrap(tools.NewWriteFile(m.home).Spec()), `{"path":"keep.txt","content":"after"}`)
	if record, err := turn.Complete(ctx); err != nil || record == nil {
		t.Fatalf("complete: %+v, %v", record, err)
	}
	writeCheckpointFixture(t, filepath.Join(data, "sessions.db"), "conversations remain")
	writeCheckpointFixture(t, filepath.Join(data, "projects", "memory.db"), "memory remains")
	archive := filepath.Join(data, "badcheckpoints", workspaceCheckpointKey(m.home))
	writeCheckpointFixture(t, filepath.Join(archive, "turns.json"), "[]")
	writeCheckpointFixture(t, filepath.Join(archive, "objects.git", "objects", "legacy.bin"), "opaque archived bytes")
	if err := os.Chmod(filepath.Join(archive, "objects.git", "objects", "legacy.bin"), 0444); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(data, "checkpoints", workspaceCheckpointKey(t.TempDir()))
	writeCheckpointFixture(t, filepath.Join(other, "turns.json"), "[]")
	writeCheckpointFixture(t, filepath.Join(other, "retained.bin"), "other project")
	preview, err := PreviewWorkspaceCleanup(ctx, m.home, data)
	if err != nil || preview.Stores != 2 || preview.Files == 0 || preview.Bytes == 0 {
		t.Fatalf("preview: %+v, %v", preview, err)
	}
	removed, err := ClearWorkspaceCheckpoints(ctx, m.home, data)
	if err != nil || removed != preview {
		t.Fatalf("removed: %+v, want %+v, %v", removed, preview, err)
	}
	for _, path := range []string{filepath.Dir(m.repo), archive} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("selected store remains: %s: %v", path, err)
		}
	}
	assertFileBytes(t, filepath.Join(m.home, "keep.txt"), []byte("after"))
	assertFileBytes(t, filepath.Join(data, "sessions.db"), []byte("conversations remain"))
	assertFileBytes(t, filepath.Join(data, "projects", "memory.db"), []byte("memory remains"))
	assertFileBytes(t, filepath.Join(other, "retained.bin"), []byte("other project"))
	if got, err := PreviewWorkspaceCleanup(ctx, m.home, data); err != nil || got.Stores != 0 || got.Bytes != 0 {
		t.Fatalf("empty preview initialized a store: %+v, %v", got, err)
	}
	transaction, err := m.gate.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	counter, err := NewStoreUsageCounter(m.gate)
	if err != nil {
		t.Fatal(err)
	}
	state, err := counter.readLocked()
	transaction.Close()
	if err != nil || !state.Dirty {
		t.Fatalf("cleanup trusted old usage baseline: %+v, %v", state, err)
	}
	// Fresh captures from a retained conversation can recreate history; clearing
	// checkpoints does not disable Undo globally or leave the cached index ready.
	// Do not call Latest first: NewTurn's capture must reload and reset the stale
	// Manager itself, as it does through retained TUI tool closures.
	next := m.NewTurn("shared-conversation-two", "edit again")
	runSnapshotTool(t, next.Wrap(tools.NewWriteFile(m.home).Spec()), `{"path":"keep.txt","content":"again"}`)
	nextRecord, err := next.Complete(ctx)
	if err != nil || nextRecord == nil {
		t.Fatalf("cached manager could not capture after cleanup: %+v, %v", nextRecord, err)
	}
	if len(m.records) != 1 || m.records[0].ID != nextRecord.ID || m.Latest("shared-conversation-one") != nil {
		t.Fatalf("external cleanup resurrected cached records: %+v", m.records)
	}
	if _, err := m.Undo(ctx, nextRecord.ID); err != nil {
		t.Fatalf("fresh checkpoint Undo after external cleanup: %v", err)
	}
	assertFileBytes(t, filepath.Join(m.home, "keep.txt"), []byte("after"))
}

func TestWorkspaceCleanupCanceledAdmissionPreservesAllData(t *testing.T) {
	m, data := cleanupFixture(t)
	path := filepath.Join(filepath.Dir(m.repo), "sentinel")
	writeCheckpointFixture(t, path, "kept")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PreviewWorkspaceCleanup(ctx, m.home, data); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled preview: %v", err)
	}
	if _, err := ClearWorkspaceCheckpoints(ctx, m.home, data); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled cleanup: %v", err)
	}
	if err := m.ClearContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled manager clear: %v", err)
	}
	assertFileBytes(t, path, []byte("kept"))
	if _, err := os.Stat(filepath.Join(data, storeUsageName)); !os.IsNotExist(err) {
		t.Fatalf("canceled admission wrote a usage receipt: %v", err)
	}
}

func TestWorkspaceCleanupProtectsLiveOwnerBeforeFirstSnapshot(t *testing.T) {
	m, data := cleanupFixture(t)
	turn, barrier := bindingFixtureTurn(t, m)
	borrow, err := BorrowInvocation(WithTurn(context.Background(), turn, barrier), true)
	if err != nil {
		t.Fatal(err)
	}
	defer borrow.Close()
	for _, fn := range []func(context.Context, string, string) (WorkspaceCleanupPreview, error){PreviewWorkspaceCleanup, ClearWorkspaceCheckpoints} {
		if _, err := fn(context.Background(), m.home, data); !errors.Is(err, ErrActiveTurn) {
			t.Fatalf("live owner admitted cleanup: %v", err)
		}
	}
	if err := m.ClearContext(context.Background()); !errors.Is(err, ErrActiveTurn) {
		t.Fatalf("live owner admitted manager clear: %v", err)
	}
	borrow.Close()
	if _, err := turn.Complete(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceCleanupUnknownArchiveAndPreparedJournalFailClosed(t *testing.T) {
	for _, scenario := range []string{"unknown archive", "malformed archive", "prepared journal"} {
		t.Run(scenario, func(t *testing.T) {
			m, data := cleanupFixture(t)
			sentinel := filepath.Join(filepath.Dir(m.repo), "sentinel")
			writeCheckpointFixture(t, sentinel, "kept")
			archive := filepath.Join(data, "badcheckpoints", workspaceCheckpointKey(m.home))
			if scenario == "prepared journal" {
				writeCheckpointFixture(t, filepath.Join(data, filepath.FromSlash(retentionJournalName)), "{}")
			} else {
				writeCheckpointFixture(t, filepath.Join(archive, "sentinel"), "archive kept")
				if scenario == "malformed archive" {
					writeCheckpointFixture(t, filepath.Join(archive, "turns.json"), "[")
				}
			}
			if _, err := ClearWorkspaceCheckpoints(context.Background(), m.home, data); err == nil {
				t.Fatal("cleanup accepted unknown pending state")
			}
			assertFileBytes(t, sentinel, []byte("kept"))
			if scenario != "prepared journal" {
				assertFileBytes(t, filepath.Join(archive, "sentinel"), []byte("archive kept"))
			}
		})
	}
}

func TestWorkspaceCleanupArchivedActiveRootsProtectBothStores(t *testing.T) {
	m, data := cleanupFixture(t)
	writeCheckpointFixture(t, filepath.Join(m.home, "a.txt"), "before")
	turn := m.NewTurn("session", "edit")
	runSnapshotTool(t, turn.Wrap(tools.NewWriteFile(m.home).Spec()), `{"path":"a.txt","content":"after"}`)
	record, err := turn.Complete(context.Background())
	if err != nil || record == nil {
		t.Fatalf("complete: %+v, %v", record, err)
	}
	archive := filepath.Join(data, "badcheckpoints", workspaceCheckpointKey(m.home))
	if err := os.MkdirAll(filepath.Dir(archive), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Dir(m.repo), archive); err != nil {
		t.Fatal(err)
	}
	guard := &Manager{home: data, repo: filepath.Join(archive, "objects.git"), gate: m.gate}
	if _, err := guard.git(context.Background(), "update-ref", "refs/supercli/active/unknown-owner/before", record.Before); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(filepath.Dir(m.repo), "sentinel")
	writeCheckpointFixture(t, sentinel, "main kept")
	if _, err := ClearWorkspaceCheckpoints(context.Background(), m.home, data); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("archived pending roots admitted cleanup: %v", err)
	}
	assertFileBytes(t, sentinel, []byte("main kept"))
	if _, err := os.Stat(filepath.Join(archive, "turns.json")); err != nil {
		t.Fatalf("archived records lost: %v", err)
	}
}

func TestWorkspaceCleanupRejectsLaterSymlinkOrJunction(t *testing.T) {
	for _, where := range []string{"parent", "store", "descendant"} {
		t.Run(where, func(t *testing.T) {
			m, data := cleanupFixture(t)
			outside := t.TempDir()
			writeCheckpointFixture(t, filepath.Join(outside, "sentinel"), "outside kept")
			root := filepath.Dir(m.repo)
			switch where {
			case "parent":
				if err := os.RemoveAll(filepath.Dir(root)); err != nil {
					t.Fatal(err)
				}
				root = filepath.Dir(root)
			case "store":
				if err := os.Remove(root); err != nil {
					t.Fatal(err)
				}
			case "descendant":
				root = filepath.Join(root, "linked")
			}
			if err := os.Symlink(outside, root); err != nil {
				t.Skipf("native directory links unavailable: %v", err)
			}
			for _, fn := range []func(context.Context, string, string) (WorkspaceCleanupPreview, error){PreviewWorkspaceCleanup, ClearWorkspaceCheckpoints} {
				if _, err := fn(context.Background(), m.home, data); !errors.Is(err, ErrStoreInventory) {
					t.Fatalf("%s link admitted cleanup: %v", where, err)
				}
			}
			assertFileBytes(t, filepath.Join(outside, "sentinel"), []byte("outside kept"))
		})
	}
}

func TestWorkspaceCleanupLinkAliasUsesItsOwnOpenKey(t *testing.T) {
	m, data := cleanupFixture(t)
	alias := filepath.Join(t.TempDir(), "linked-workspace")
	if err := os.Symlink(m.home, alias); err != nil {
		t.Skipf("native directory links unavailable: %v", err)
	}
	a, err := Open(alias, data)
	if err != nil {
		t.Fatal(err)
	}
	if a.repo == m.repo {
		t.Fatal("fixture alias has no separate Open key")
	}
	writeCheckpointFixture(t, m.meta, "[]")
	writeCheckpointFixture(t, a.meta, "[]")
	writeCheckpointFixture(t, filepath.Join(filepath.Dir(m.repo), "target-kept"), "target history")
	writeCheckpointFixture(t, filepath.Join(filepath.Dir(a.repo), "alias-only"), "alias history")
	preview, err := PreviewWorkspaceCleanup(context.Background(), alias, data)
	if err != nil || preview.Workspace != alias || preview.Stores != 1 || preview.Bytes != int64(len("[]alias history")) {
		t.Fatalf("alias preview selected target history: %+v, %v", preview, err)
	}
	if _, err := ClearWorkspaceCheckpoints(context.Background(), alias, data); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, filepath.Join(filepath.Dir(m.repo), "target-kept"), []byte("target history"))
	if _, err := os.Lstat(filepath.Dir(a.repo)); !os.IsNotExist(err) {
		t.Fatalf("own alias history remains: %v", err)
	}
}

func TestWorkspaceCleanupCanceledWhileWaitingForGate(t *testing.T) {
	m, data := cleanupFixture(t)
	sentinel := filepath.Join(filepath.Dir(m.repo), "sentinel")
	writeCheckpointFixture(t, sentinel, "kept")
	transaction, err := m.gate.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Close()
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		_, err := ClearWorkspaceCheckpoints(ctx, m.home, data)
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("gate waiter ignored cancellation: %v", err)
	}
	assertFileBytes(t, sentinel, []byte("kept"))
}

type cancelAfterCleanupFile struct {
	context.Context
	cancel context.CancelFunc
	path   string
}

func (c cancelAfterCleanupFile) Err() error {
	if _, err := os.Lstat(c.path); os.IsNotExist(err) {
		c.cancel()
	}
	return c.Context.Err()
}

func TestWorkspaceCleanupMidDeleteCancellationIsReportedAndRetryable(t *testing.T) {
	m, data := cleanupFixture(t)
	first := filepath.Join(filepath.Dir(m.repo), "first")
	writeCheckpointFixture(t, first, "removed first")
	writeCheckpointFixture(t, filepath.Join(filepath.Dir(m.repo), "second"), "remaining")
	writeCheckpointFixture(t, m.meta, "[]")
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := cancelAfterCleanupFile{Context: base, cancel: cancel, path: first}
	partial, err := ClearWorkspaceCheckpoints(ctx, m.home, data)
	if !errors.Is(err, context.Canceled) || partial.Files == 0 || partial.Stores != 0 {
		t.Fatalf("partial deletion claimed completion: %+v, %v", partial, err)
	}
	if _, err := os.Lstat(filepath.Dir(m.repo)); err != nil {
		t.Fatalf("cancellation failed to preserve remaining store: %v", err)
	}
	if _, err := ClearWorkspaceCheckpoints(context.Background(), m.home, data); err != nil {
		t.Fatalf("retry after interrupted explicit cleanup: %v", err)
	}
	if _, err := os.Lstat(filepath.Dir(m.repo)); !os.IsNotExist(err) {
		t.Fatalf("retry left selected store: %v", err)
	}
}
