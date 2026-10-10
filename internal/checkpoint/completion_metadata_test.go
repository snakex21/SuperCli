package checkpoint

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func completionMetadataFixture(t *testing.T) (*Manager, CompletionIdentity, Record) {
	t.Helper()
	m := openSnapshotTestManager(t, t.TempDir())
	owner := CompletionIdentity{Key: strings.Repeat("a", 32), SessionID: "original", UserSeq: 1, UserMessageID: 11}
	r := Record{ID: "record", CompletionKey: owner.Key, SessionID: owner.SessionID, UserSeq: owner.UserSeq, UserMessageID: owner.UserMessageID,
		Changes: []FileChange{{Path: "a.txt", Kind: "modified"}}}
	m.SetUserReceiptValidator(func(_ context.Context, sid string, seq int, id int64) (bool, error) {
		return sid == owner.SessionID && seq == owner.UserSeq && id == owner.UserMessageID, nil
	})
	return m, owner, r
}

func writeCompletionMetadata(t *testing.T, m *Manager, records []Record) {
	t.Helper()
	data, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.meta, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCompletionMetadataReadsFreshExactOwnersWithoutGit(t *testing.T) {
	m, owner, record := completionMetadataFixture(t)
	writeCompletionMetadata(t, m, []Record{record})
	t.Setenv("PATH", t.TempDir()) // Any new Git invocation would fail.
	calls := 0
	apply := func(got CompletionIdentity, r Record) error {
		calls++
		if got != owner || !reflect.DeepEqual(r.Changes, record.Changes) {
			t.Fatal("incorrect owner/changes")
		}
		return nil
	}
	if err := m.WithCompletionRecords(context.Background(), []CompletionIdentity{owner}, apply); err != nil || calls != 1 {
		t.Fatalf("read: calls=%d err=%v", calls, err)
	}
	// An independent writer removed the record. A stale in-memory manager must
	// not resurrect it, infer current workspace changes or recreate metadata.
	writeCompletionMetadata(t, m, nil)
	if err := m.WithCompletionRecords(context.Background(), []CompletionIdentity{owner}, apply); err != nil || calls != 1 {
		t.Fatalf("expired record reused: calls=%d err=%v", calls, err)
	}
	raw, err := os.ReadFile(m.meta)
	if err != nil || string(raw) != "null" {
		t.Fatalf("read-only repair rewrote metadata: %q %v", raw, err)
	}
}

func TestCompletionMetadataRejectsDuplicatesLegacyAndChangedReceipt(t *testing.T) {
	for _, kind := range []string{"duplicate", "legacy", "other-session", "other-receipt", "changed-user"} {
		t.Run(kind, func(t *testing.T) {
			m, owner, record := completionMetadataFixture(t)
			records := []Record{record}
			switch kind {
			case "duplicate":
				records = append(records, record, record)
			case "legacy":
				records[0].CompletionKey = ""
			case "other-session":
				records[0].SessionID = "replacement"
			case "other-receipt":
				records[0].UserMessageID++
			case "changed-user":
				m.SetUserReceiptValidator(func(context.Context, string, int, int64) (bool, error) { return false, nil })
			}
			writeCompletionMetadata(t, m, records)
			if err := m.WithCompletionRecords(context.Background(), []CompletionIdentity{owner}, func(CompletionIdentity, Record) error { t.Fatal("foreign/stale record admitted"); return nil }); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCompletionMetadataSkipsBusyPreparedOrMalformedStore(t *testing.T) {
	m, owner, record := completionMetadataFixture(t)
	ctx := context.Background()
	writeCompletionMetadata(t, m, []Record{record})
	apply := func(CompletionIdentity, Record) error { t.Fatal("unsafe callback admitted"); return nil }
	lease, err := m.gate.TryAcquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.WithCompletionRecords(ctx, []CompletionIdentity{owner}, apply); !errors.Is(err, ErrStoreBusy) {
		t.Fatalf("busy gate=%v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(filepath.Dir(m.gate.path), filepath.FromSlash(retentionJournalName))
	if err := os.WriteFile(journal, []byte("prepared transaction"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.WithCompletionRecords(ctx, []CompletionIdentity{owner}, apply); !errors.Is(err, ErrStoreBusy) {
		t.Fatalf("prepared journal=%v", err)
	}
	if err := os.Remove(journal); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.meta, []byte("malformed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.WithCompletionRecords(ctx, []CompletionIdentity{owner}, apply); err == nil {
		t.Fatal("malformed metadata admitted")
	}
	if err := m.WithCompletionRecords(ctx, nil, apply); err != nil {
		t.Fatalf("empty owners performed I/O: %v", err)
	}
}

func TestDeferredCompletionPreparesKeyBeforeLeaseAndPreservesIt(t *testing.T) {
	m := openSnapshotTestManager(t, t.TempDir())
	turn := m.NewTurn("session", "no-op accepted worker")
	borrow, err := turn.barrier.Borrow(context.Background()) // Admission precedes lease setup.
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	if r, err := turn.CompleteDeferred(context.Background(), func(_ *Record, err error) { done <- err }); err != nil || r != nil {
		t.Fatalf("deferred=%+v %v", r, err)
	}
	key := turn.CompletionKey()
	if len(key) != 32 || turn.active == nil || turn.active.id != key {
		t.Fatal("key not prepared before finalizer")
	}
	if _, err := turn.CompleteDeferred(context.Background(), nil); err != nil || turn.CompletionKey() != key {
		t.Fatal("repeated completion changed identity")
	}
	borrow.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if turn.CompletionKey() != key {
		t.Fatal("drained no-op lost identity")
	}
}
