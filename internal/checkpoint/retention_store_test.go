package checkpoint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func retentionFixture(t *testing.T) (*Manager, string, Record, Record) {
	t.Helper()
	home, data := t.TempDir(), t.TempDir()
	m, err := Open(home, data)
	if errors.Is(err, ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	before, after := make([]byte, 32<<10), make([]byte, 32<<10)
	if _, err := rand.New(rand.NewSource(13)).Read(before); err != nil {
		t.Fatal(err)
	}
	if _, err := rand.New(rand.NewSource(23)).Read(after); err != nil {
		t.Fatal(err)
	}
	old := recordParallelStoreFileChange(t, m, "old", "session", "old.bin", 1, before, after)
	newest := recordParallelStoreFileChange(t, m, "new", "session", "new.bin", 2, []byte("new-before"), []byte("new-after"))
	return m, data, old, newest
}

func retentionTransaction(t *testing.T, m *Manager, fn func()) {
	t.Helper()
	lease, err := m.gate.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lease.Close(); err != nil {
			t.Error(err)
		}
	}()
	fn()
}

func retentionNewestTarget(t *testing.T, audit *StoreRetentionAudit, key RetentionKey) int64 {
	t.Helper()
	units := map[string]int64{}
	for _, unit := range audit.Inventory.Fixed {
		units[unit.Path] = unit.Bytes
	}
	for _, record := range audit.Inventory.Records {
		if record.Key == key {
			for _, unit := range record.Units {
				units[unit.Path] = unit.Bytes
			}
		}
	}
	total := int64(0)
	for _, size := range units {
		total += size
	}
	return total
}

func TestStoreRetentionExpiresOldestAndKeepsExactUndoRedo(t *testing.T) {
	m, data, old, newest := retentionFixture(t)
	oldLive, err := os.ReadFile(filepath.Join(m.home, "old.bin"))
	if err != nil {
		t.Fatal(err)
	}
	retentionTransaction(t, m, func() {
		audit, err := AuditStoreRetentionLocked(context.Background(), data)
		if err != nil || len(audit.Inventory.Records) != 2 || len(audit.Protected) != 0 {
			t.Fatalf("audit=%+v err=%v", audit, err)
		}
		target := retentionNewestTarget(t, audit, RetentionKey{filepath.ToSlash(filepath.Dir(m.meta)[len(data)+1:]), newest.ID})
		result, err := EnforceStoreBudgetLocked(context.Background(), data, target, 0)
		if err != nil || len(result.Expired) != 1 || result.Expired[0].ID != old.ID || result.MeasuredBytes > target || result.ReclaimedBytes == 0 {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})
	if current, err := os.ReadFile(filepath.Join(m.home, "old.bin")); err != nil || !bytes.Equal(oldLive, current) {
		t.Fatalf("live expired file changed: %v", err)
	}
	if _, err := m.Undo(context.Background(), newest.ID); err != nil {
		t.Fatal(err)
	}
	if current, err := os.ReadFile(filepath.Join(m.home, "new.bin")); err != nil || !bytes.Equal(current, []byte("new-before")) {
		t.Fatalf("retained undo bytes=%q err=%v", current, err)
	}
	if _, err := m.Redo(context.Background(), newest.ID); err != nil {
		t.Fatal(err)
	}
	if current, err := os.ReadFile(filepath.Join(m.home, "new.bin")); err != nil || !bytes.Equal(current, []byte("new-after")) {
		t.Fatalf("retained redo bytes=%q err=%v", current, err)
	}
}

func TestStoreRetentionUnknownRefIndexAndReflogAreProtected(t *testing.T) {
	for _, source := range []string{"ref", "index", "reflog", "HEAD"} {
		t.Run(source, func(t *testing.T) {
			m, data, old, _ := retentionFixture(t)
			retentionTransaction(t, m, func() {
				switch source {
				case "ref":
					if _, err := m.git(context.Background(), "update-ref", "refs/foreign/keep", old.Before); err != nil {
						t.Fatal(err)
					}
				case "index":
					if _, err := m.git(context.Background(), "read-tree", old.Before); err != nil {
						t.Fatal(err)
					}
				case "reflog":
					path := filepath.Join(m.repo, "logs", "refs", "foreign", "keep")
					if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
						t.Fatal(err)
					}
					line := old.Before + " " + old.After + " Fixture <fixture@local> 1 +0000\tkeep exact old root\n"
					if err := os.WriteFile(path, []byte(line), 0600); err != nil {
						t.Fatal(err)
					}
				case "HEAD":
					if err := os.WriteFile(filepath.Join(m.repo, "HEAD"), []byte(old.Before+"\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				audit, err := AuditStoreRetentionLocked(context.Background(), data)
				if err != nil || len(audit.Inventory.Records) != 2 || len(audit.Protected) != 0 {
					t.Fatalf("root source was not understood: %+v %v", audit, err)
				}
				floor, err := PlanStoreRetention(audit.Inventory, 0)
				if !errors.Is(err, ErrStoreBudget) || floor.ProtectedBytes < 32<<10 {
					t.Fatalf("%s root omitted: %+v %v", source, floor, err)
				}
				original, _ := os.ReadFile(m.meta)
				result, err := EnforceStoreBudgetLocked(context.Background(), data, floor.ProtectedBytes-1, 0)
				current, _ := os.ReadFile(m.meta)
				if !errors.Is(err, ErrStoreBudget) || len(result.Expired) != 0 || !bytes.Equal(current, original) {
					t.Fatalf("unattainable cap expired history: %+v %v", result, err)
				}
			})
		})
	}
}

func TestStoreRetentionUnknownRepositoryIsMeasuredAndRetained(t *testing.T) {
	m, data, _, _ := retentionFixture(t)
	retentionTransaction(t, m, func() {
		if err := os.WriteFile(m.meta, []byte("incomplete-metadata"), 0600); err != nil {
			t.Fatal(err)
		}
		audit, err := AuditStoreRetentionLocked(context.Background(), data)
		if err != nil || len(audit.Protected) != 1 || len(audit.Inventory.Records) != 0 || len(audit.Inventory.Unreferenced) != 0 {
			t.Fatalf("audit=%+v err=%v", audit, err)
		}
		plan, err := PlanStoreRetention(audit.Inventory, 0)
		if !errors.Is(err, ErrStoreBudget) || plan.BeforeBytes != plan.ProtectedBytes || plan.BeforeBytes < 64<<10 {
			t.Fatalf("unknown store omitted: %+v %v", plan, err)
		}
	})
}

func TestStoreRetentionJournalPreservesUnknownFieldsAndResumes(t *testing.T) {
	m, data, old, newest := retentionFixture(t)
	retentionTransaction(t, m, func() {
		raw, err := os.ReadFile(m.meta)
		if err != nil {
			t.Fatal(err)
		}
		var objects []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &objects); err != nil {
			t.Fatal(err)
		}
		objects[1]["future_extension"] = json.RawMessage(`{"exact_integer":9007199254740993123,"unknown":[true,"keep"]}`)
		objects[1]["raw_bytes"] = json.RawMessage("false")
		raw, err = json.Marshal(objects)
		if err != nil || os.WriteFile(m.meta, raw, 0600) != nil {
			t.Fatal(err)
		}
		audit, err := AuditStoreRetentionLocked(context.Background(), data)
		if err != nil {
			t.Fatal(err)
		}
		store := filepath.ToSlash(filepath.Dir(m.meta)[len(data)+1:])
		plan := RetentionPlan{Expired: []RetentionKey{{Store: store, ID: old.ID}}}
		journal, err := makeRetentionJournal(audit, plan)
		if err != nil || writeRetentionJournal(data, journal) != nil {
			t.Fatal(err)
		}
		// Simulate interruption after metadata replacement but before the journal
		// phase/ref update. Resume recognizes the exact replacement hash.
		if err := retentionAtomicWrite(data, m.meta, journal.Repos[0].Next); err != nil {
			t.Fatal(err)
		}
		if err := resumeRetentionJournalLocked(context.Background(), data); err != nil {
			t.Fatal(err)
		}
		current, err := os.ReadFile(m.meta)
		if err != nil {
			t.Fatal(err)
		}
		var kept []map[string]json.RawMessage
		if err := json.Unmarshal(current, &kept); err != nil || len(kept) != 1 || !bytes.Equal(kept[0]["future_extension"], objects[1]["future_extension"]) || string(kept[0]["raw_bytes"]) != "false" {
			t.Fatalf("unknown/legacy metadata changed: %s err=%v", current, err)
		}
		refs := &StoreRetentionAudit{}
		if err := refs.gitTokens(context.Background(), m.repo, nil, false, func(line string) error {
			if strings.Contains(line, recordRefRoot(old.ID)) {
				t.Fatalf("expired ref survived journal: %s", line)
			}
			return nil
		}, "for-each-ref", "--format=%(refname)"); err != nil {
			t.Fatal(err)
		}
		if _, err := m.git(context.Background(), "cat-file", "-e", newest.After); err != nil {
			t.Fatal(err)
		}
	})
}

func TestStoreRetentionJournalRejectsChangedMetadataWithoutOverwrite(t *testing.T) {
	m, data, old, _ := retentionFixture(t)
	retentionTransaction(t, m, func() {
		a, err := AuditStoreRetentionLocked(context.Background(), data)
		if err != nil {
			t.Fatal(err)
		}
		store, _ := filepath.Rel(data, filepath.Dir(m.meta))
		j, err := makeRetentionJournal(a, RetentionPlan{Expired: []RetentionKey{{Store: filepath.ToSlash(store), ID: old.ID}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := writeRetentionJournal(data, j); err != nil {
			t.Fatal(err)
		}
		current, err := os.ReadFile(m.meta)
		if err != nil {
			t.Fatal(err)
		}
		var records []map[string]json.RawMessage
		if err := json.Unmarshal(current, &records); err != nil {
			t.Fatal(err)
		}
		records[1]["undone"] = json.RawMessage("true")
		changed, err := json.Marshal(records)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(m.meta, changed, 0600); err != nil {
			t.Fatal(err)
		}
		if err := resumeRetentionJournalLocked(context.Background(), data); !errors.Is(err, ErrStoreInventory) {
			t.Fatal("stale journal accepted:", err)
		}
		current, err = os.ReadFile(m.meta)
		if err != nil || !bytes.Equal(current, changed) {
			t.Fatal("stale journal overwrote changed metadata:", err)
		}
		if _, err := os.Lstat(filepath.Join(filepath.Dir(m.meta), retentionExpiryName)); !os.IsNotExist(err) {
			t.Fatal("rejected transaction wrote a frontier:", err)
		}
		if _, err := m.git(context.Background(), "cat-file", "-e", old.Before); err != nil {
			t.Fatal("stale journal removed original objects:", err)
		}
	})
}

func TestStoreRetentionCancellationPreservesOriginalRecords(t *testing.T) {
	m, data, _, _ := retentionFixture(t)
	retentionTransaction(t, m, func() {
		original, err := os.ReadFile(m.meta)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err = EnforceStoreBudgetLocked(ctx, data, DefaultStoreBudgetBytes, 0)
		current, readErr := os.ReadFile(m.meta)
		if !errors.Is(err, context.Canceled) || readErr != nil || !bytes.Equal(original, current) {
			t.Fatalf("canceled collection changed records: %v %v", err, readErr)
		}
	})
}

func TestStoreRetentionUnderBudgetDoesNotChangeMetadataOrRefs(t *testing.T) {
	m, data, _, _ := retentionFixture(t)
	retentionTransaction(t, m, func() {
		metadata, err := os.ReadFile(m.meta)
		if err != nil {
			t.Fatal(err)
		}
		refs, err := m.git(context.Background(), "for-each-ref", "--format=%(refname) %(objectname)")
		if err != nil {
			t.Fatal(err)
		}
		result, err := EnforceStoreBudgetLocked(context.Background(), data, DefaultStoreBudgetBytes, 0)
		if err != nil || !result.PhysicalComplete || result.FullCensusComplete || len(result.Expired) != 0 || result.ReclaimedBytes != 0 || result.MeasuredBytes <= 0 || result.MeasuredBytes > DefaultStoreBudgetBytes {
			t.Fatalf("unexpected under-budget mutation: %+v %v", result, err)
		}
		current, err := os.ReadFile(m.meta)
		currentRefs, refErr := m.git(context.Background(), "for-each-ref", "--format=%(refname) %(objectname)")
		if err != nil || refErr != nil || !bytes.Equal(current, metadata) || currentRefs != refs {
			t.Fatalf("under-budget metadata/ref write: %v %v", err, refErr)
		}
	})
}

func TestStoreRetentionUnderBudgetSkipsGitAndMetadataParsing(t *testing.T) {
	m, data, _, _ := retentionFixture(t)
	retentionTransaction(t, m, func() {
		// Under a known physical cap, reachability and malformed metadata do not
		// justify a deletion. Prove the cheap exit does not even invoke Git.
		original := []byte("synthetic incomplete metadata retained exactly")
		if err := os.WriteFile(m.meta, original, 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", t.TempDir())
		result, err := EnforceStoreBudgetLocked(context.Background(), data, DefaultStoreBudgetBytes, 0)
		current, readErr := os.ReadFile(m.meta)
		if err != nil || readErr != nil || !result.PhysicalComplete || result.FullCensusComplete || len(result.Expired) != 0 || !bytes.Equal(current, original) {
			t.Fatalf("cheap physical exit failed: %+v %v %v", result, err, readErr)
		}
	})
}
