package checkpoint

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercli/internal/tools"
)

func recordRefsTestListing(t *testing.T, m *Manager, root string) string {
	t.Helper()
	out, err := m.git(context.Background(), "for-each-ref", "--format=%(refname) %(objectname)", root)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

func TestFailedTurnAppendRollsBackNewRecordRefsWithoutLosingBaseline(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	m := openSnapshotTestManager(t, home)
	seed := recordParallelStoreFileChange(t, m, "retained", "other", "other.bin", 1, []byte("old"), []byte("new"))
	refsBefore := recordRefsTestListing(t, m, "refs/supercli/records/")
	metadataBefore, err := os.ReadFile(m.meta)
	if err != nil {
		t.Fatal(err)
	}
	before := []byte{0xff, 0, '\r', '\n', 0xfe}
	after := []byte("synthetic after\r\n\x00")
	path := filepath.Join(home, "source.bin")
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	turn := m.NewTurn("synthetic-failed-admission", "edit one file")
	turn.SetUserSeq(7)
	blocked := m.meta + ".tmp"
	sentinel := filepath.Join(blocked, "synthetic-sentinel")
	// Register before admission: any assertion failure must drain this private
	// fixture and close its native lease before TempDir cleanup starts.
	t.Cleanup(func() {
		_ = os.Remove(sentinel)
		_ = os.Remove(blocked)
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		defer func() {
			// Only this disposable, synchronous fixture: if cleanup itself
			// fails, close its handle without deleting any recovery refs.
			if turn.active != nil && turn.active.lease != nil && turn.active.lease.owner != nil {
				if err := turn.active.lease.owner.Close(); err != nil {
					t.Errorf("close synthetic checkpoint lease: %v", err)
				}
			}
		}()
		if _, err := turn.Complete(cleanupCtx); err != nil {
			t.Errorf("complete synthetic checkpoint fixture: %v", err)
		}
	})
	args, err := json.Marshal(map[string]string{"path": "source.bin"})
	if err != nil {
		t.Fatal(err)
	}
	// write_file itself correctly refuses existing binary files. This fixture
	// tests checkpoint bytes/scope, so use a real file write behind its named
	// checkpoint wrapper rather than the production text editor.
	write := tools.Tool{Name: "write_file", Fn: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
		if err := ctx.Err(); err != nil {
			return tools.Result{Err: err}, nil
		}
		return tools.Result{Err: os.WriteFile(path, after, 0o600)}, nil
	}}
	runSnapshotTool(t, turn.Wrap(write), string(args))
	// Fail after refs are published, rather than at the initial metadata read.
	// Nonempty is essential: saveLocked's deferred Remove must not clear it.
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sentinel, []byte("keep directory nonempty"), 0o600); err != nil {
		t.Fatal(err)
	}
	if record, err := turn.Complete(ctx); err == nil || record != nil {
		t.Fatalf("failed metadata admission: record=%+v err=%v", record, err)
	}
	if got := recordRefsTestListing(t, m, "refs/supercli/records/"); got != refsBefore {
		t.Fatalf("failed append leaked new refs or removed retained ones: %s", got)
	}
	metadataAfter, err := os.ReadFile(m.meta)
	if err != nil || !bytes.Equal(metadataAfter, metadataBefore) {
		t.Fatalf("failed append changed durable metadata: %v", err)
	}
	active := recordRefsTestListing(t, m, turn.active.root())
	if !strings.Contains(active, turn.active.root()+"/before "+turn.before) || !strings.Contains(active, turn.active.root()+"/after "+turn.snapshotAfter) || pendingOwnerCount(t, m) != 1 {
		t.Fatal("failed append lost active snapshots or its retry owner")
	}
	if out, err := m.git(ctx, "show", turn.before+":source.bin"); err != nil || !bytes.Equal([]byte(out), before) {
		t.Fatalf("original bytes lost: %v", err)
	}
	assertFileBytes(t, path, after)
	if err := os.Remove(sentinel); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	record, err := turn.Complete(ctx)
	if err != nil || record == nil || record.UserSeq != 7 || len(m.records) != 2 {
		t.Fatalf("safe retry: record=%+v count=%d err=%v", record, len(m.records), err)
	}
	if got := recordRefsTestListing(t, m, "refs/supercli/records/"); len(strings.Split(got, "\n")) != 4 || !strings.Contains(got, recordRefRoot(record.ID)+"/before "+record.Before) || !strings.Contains(got, recordRefRoot(seed.ID)+"/before "+seed.Before) {
		t.Fatalf("retry retained abandoned admissions: %s", got)
	}
	if recordRefsTestListing(t, m, "refs/supercli/active/") != "" || pendingOwnerCount(t, m) != 0 {
		t.Fatal("successful retry retained active work")
	}
	if _, err := m.Undo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, path, before)
	assertFileBytes(t, filepath.Join(home, "other.bin"), []byte("new"))
	if _, err := m.Redo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, path, after)
}

func TestAppendRejectsExistingRecordIDBeforePinning(t *testing.T) {
	ctx := context.Background()
	m := openSnapshotTestManager(t, t.TempDir())
	r := recordParallelStoreFileChange(t, m, "same-id", "original", "source.bin", 1, []byte("before"), []byte("after"))
	refsBefore := recordRefsTestListing(t, m, recordRefRoot(r.ID))
	metadataBefore, err := os.ReadFile(m.meta)
	if err != nil {
		t.Fatal(err)
	}
	r.Before, r.After = r.After, r.Before
	r.SessionID = "replacement"
	committed, err := m.appendCommitted(ctx, r)
	if committed || err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("reused record ID accepted: committed=%v err=%v", committed, err)
	}
	metadataAfter, err := os.ReadFile(m.meta)
	if err != nil || !bytes.Equal(metadataAfter, metadataBefore) || recordRefsTestListing(t, m, recordRefRoot(r.ID)) != refsBefore {
		t.Fatalf("reused ID changed existing metadata or refs: %v", err)
	}
	if _, err := m.Undo(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, filepath.Join(m.home, "source.bin"), []byte("before"))
}

func TestAppendRejectsOrphanRecordRefPairs(t *testing.T) {
	for _, side := range []string{"before", "after"} {
		t.Run(side, func(t *testing.T) {
			ctx := context.Background()
			m := openSnapshotTestManager(t, t.TempDir())
			writeCheckpointFixture(t, filepath.Join(m.home, "source.bin"), "snapshot")
			commit, err := m.captureSnapshot(ctx, []string{"source.bin"}, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			r := Record{ID: "orphan", Before: commit, After: commit}
			if _, err := m.git(ctx, "update-ref", recordRefRoot(r.ID)+"/"+side, commit); err != nil {
				t.Fatal(err)
			}
			refsBefore := recordRefsTestListing(t, m, recordRefRoot(r.ID))
			committed, err := m.appendCommitted(ctx, r)
			if committed || err == nil || len(m.records) != 0 || recordRefsTestListing(t, m, recordRefRoot(r.ID)) != refsBefore {
				t.Fatalf("existing recovery refs replaced: committed=%v err=%v", committed, err)
			}
		})
	}
}

func TestUnknownRecordRefChildSurvivesFailedAppendAndForget(t *testing.T) {
	ctx := context.Background()
	m := openSnapshotTestManager(t, t.TempDir())
	path := filepath.Join(m.home, "source.bin")
	writeCheckpointFixture(t, path, "before")
	before, err := m.captureSnapshot(ctx, []string{"source.bin"}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	writeCheckpointFixture(t, path, "after")
	after, err := m.captureSnapshot(ctx, []string{"source.bin"}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	r := Record{ID: "unknown-child", SessionID: "synthetic", UserSeq: 1, Before: before, After: after}
	unknownRef := recordRefRoot(r.ID) + "/other-owner"
	if _, err := m.git(ctx, "update-ref", unknownRef, before); err != nil {
		t.Fatal(err)
	}
	unknownOnly := recordRefsTestListing(t, m, recordRefRoot(r.ID))
	if unknownOnly != unknownRef+" "+before {
		t.Fatal("unknown ref fixture has an unexpected OID")
	}
	blocked := m.meta + ".tmp"
	sentinel := filepath.Join(blocked, "synthetic-sentinel")
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sentinel, []byte("nonempty"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Remove(sentinel)
		_ = os.Remove(blocked)
	})
	if committed, err := m.appendCommitted(ctx, r); committed || err == nil {
		t.Fatalf("blocked append succeeded: committed=%v err=%v", committed, err)
	}
	if got := recordRefsTestListing(t, m, recordRefRoot(r.ID)); got != unknownOnly || len(m.records) != 0 {
		t.Fatalf("failed append changed unknown owner or stranded pair: %s", got)
	}
	if err := os.Remove(sentinel); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if committed, err := m.appendCommitted(ctx, r); !committed || err != nil {
		t.Fatalf("unrelated child blocked append: committed=%v err=%v", committed, err)
	}
	if got := recordRefsTestListing(t, m, recordRefRoot(r.ID)); len(strings.Split(got, "\n")) != 3 || !strings.Contains(got, unknownOnly) {
		t.Fatalf("successful append changed unknown owner: %s", got)
	}
	if err := m.ForgetFrom(r.SessionID, r.UserSeq); err != nil {
		t.Fatal(err)
	}
	if got := recordRefsTestListing(t, m, recordRefRoot(r.ID)); got != unknownOnly || len(m.records) != 0 {
		t.Fatalf("Forget removed unknown owner or retained record pair: %s", got)
	}
	assertFileBytes(t, path, []byte("after"))
}

func TestRecordRefTransactionsDoNotPublishOrDeletePartialPairs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := openSnapshotTestManager(t, t.TempDir())
	writeCheckpointFixture(t, filepath.Join(m.home, "source.bin"), "before")
	before, err := m.captureSnapshot(ctx, []string{"source.bin"}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	writeCheckpointFixture(t, filepath.Join(m.home, "source.bin"), "after")
	after, err := m.captureSnapshot(ctx, []string{"source.bin"}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := m.lockStore(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := unlock(); err != nil {
			t.Error(err)
		}
	}()
	r := Record{ID: "transaction", Before: before, After: after}
	afterRef := recordRefRoot(r.ID) + "/after"
	if _, err := m.git(ctx, "update-ref", afterRef, after); err != nil {
		t.Fatal(err)
	}
	initial := recordRefsTestListing(t, m, recordRefRoot(r.ID))
	if err := m.pinNewRecordLocked(ctx, r); err == nil || recordRefsTestListing(t, m, recordRefRoot(r.ID)) != initial {
		t.Fatal("compare-and-create published a partial pair over an existing ref")
	}
	if _, err := m.git(ctx, "update-ref", "-d", afterRef, after); err != nil {
		t.Fatal(err)
	}
	if err := m.pinNewRecordLocked(ctx, r); err != nil {
		t.Fatal(err)
	}
	// Simulate a changed owner while keeping both original roots available.
	if _, err := m.git(ctx, "update-ref", afterRef, before); err != nil {
		t.Fatal(err)
	}
	changed := recordRefsTestListing(t, m, recordRefRoot(r.ID))
	if err := m.rollbackNewRecordRefsLocked(r); err == nil || recordRefsTestListing(t, m, recordRefRoot(r.ID)) != changed {
		t.Fatal("rollback deleted one side despite a changed expected OID")
	}
	if _, err := m.git(ctx, "update-ref", afterRef, after); err != nil {
		t.Fatal(err)
	}
	// Cleanup uses a fresh bounded context even after its admission was canceled.
	cancel()
	if err := m.rollbackNewRecordRefsLocked(r); err != nil || recordRefsTestListing(t, m, recordRefRoot(r.ID)) != "" {
		t.Fatalf("canceled request stranded newly created roots: %v", err)
	}
}
