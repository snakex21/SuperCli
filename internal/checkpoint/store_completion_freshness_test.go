package checkpoint

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestManagedCompletionReloadsOtherManagerBeforeCleanExit(t *testing.T) {
	m, data, _, _ := retentionFixture(t)
	ctx := context.Background()
	if err := m.completeRetainedUsage(ctx, DefaultStoreBudgetBytes); err != nil {
		t.Fatal(err)
	}
	other, err := Open(m.home, data)
	if err != nil {
		t.Fatal(err)
	}
	latest := recordParallelStoreFileChange(t, other, "third", "session", "third.txt", 3, []byte("before"), []byte("after"))
	if err := m.completeRetainedUsage(ctx, DefaultStoreBudgetBytes); err != nil {
		t.Fatal(err)
	}
	if len(m.records) != 3 || m.records[2].ID != latest.ID {
		t.Fatalf("clean completion retained stale manager: %+v", m.records)
	}
}

func TestManagedCompletionReloadsCollectorMetadata(t *testing.T) {
	m, data, old, newest := retentionFixture(t)
	managedRetentionFixedFile(t, data, 128<<10)
	if err := m.completeRetainedUsage(context.Background(), 48<<10); err != nil {
		t.Fatal(err)
	}
	if len(m.records) != 1 || m.records[0].ID != newest.ID {
		t.Fatalf("expired %s still in memory: %+v", old.ID, m.records)
	}
}

func TestManagedCompletionReloadsAfterInterruptedJournalRecovery(t *testing.T) {
	m, data, old, newest := retentionFixture(t)
	retentionTransaction(t, m, func() {
		audit, err := AuditStoreRetentionLocked(context.Background(), data)
		if err != nil {
			t.Fatal(err)
		}
		store, err := filepath.Rel(data, filepath.Dir(m.meta))
		if err != nil {
			t.Fatal(err)
		}
		journal, err := makeRetentionJournal(audit, RetentionPlan{Expired: []RetentionKey{{Store: filepath.ToSlash(store), ID: old.ID}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := writeRetentionJournal(data, journal); err != nil {
			t.Fatal(err)
		}
	})
	t.Setenv("PATH", t.TempDir()) // Prepared metadata publication succeeds; Git ref cleanup cannot.
	if err := m.completeRetainedUsage(context.Background(), DefaultStoreBudgetBytes); err == nil {
		t.Fatal("missing ref-cleanup error")
	}
	raw, err := os.ReadFile(m.meta)
	if err != nil {
		t.Fatal(err)
	}
	var durable []Record
	if err := json.Unmarshal(raw, &durable); err != nil {
		t.Fatal(err)
	}
	if len(durable) != 1 || durable[0].ID != newest.ID || len(m.records) != 1 || m.records[0].ID != newest.ID {
		t.Fatalf("durable/in-memory expiry mismatch: disk=%+v memory=%+v", durable, m.records)
	}
	if _, err := os.Lstat(filepath.Join(data, filepath.FromSlash(retentionJournalName))); err != nil {
		t.Fatalf("unfinished recovery journal lost: %v", err)
	}
}
