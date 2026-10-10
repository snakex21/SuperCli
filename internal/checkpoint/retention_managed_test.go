package checkpoint

import (
	"bytes"
	"context"
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

func managedRetentionFixedFile(t *testing.T, data string, size int64) (string, []byte) {
	t.Helper()
	if size < 0 || size > 1<<20 {
		t.Fatal("invalid bounded synthetic protected floor:", size)
	}
	path := filepath.Join(data, "checkpoints", ".narrow-synthetic", "protected.bin")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	contents := bytes.Repeat([]byte{0xa7}, int(size))
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatal(err)
	}
	return path, contents
}

func TestManagedRetentionProvesFloorExpiresOldestAndKeepsExactHistory(t *testing.T) {
	m, data, old, newest := retentionFixture(t)
	const base int64 = 48 << 10
	protectedPath, protectedBytes := managedRetentionFixedFile(t, data, 128<<10)
	oldLive, err := os.ReadFile(filepath.Join(m.home, "old.bin"))
	if err != nil {
		t.Fatal(err)
	}
	retentionTransaction(t, m, func() {
		counter, err := NewStoreUsageCounter(m.gate)
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		var actual StoreBudgetResult
		completion, err := counter.CompleteManagedLocked(context.Background(), base, func(ctx context.Context) (StoreBudgetResult, error) {
			calls++
			var err error
			actual, err = EnforceManagedStoreBudgetLocked(ctx, data, base)
			return actual, err
		})
		if err != nil || !actual.FullCensusComplete || actual.ProtectedBytes <= base || len(actual.Expired) != 1 || actual.Expired[0].ID != old.ID || actual.ReclaimedBytes == 0 || actual.MeasuredBytes > base+actual.ProtectedBytes || completion.UpperBytes != actual.MeasuredBytes || completion.ProtectedFloorBytes != actual.ProtectedBytes {
			t.Fatalf("managed floor policy: completion=%+v collector=%+v err=%v", completion, actual, err)
		}
		physical, err := MeasureStoreRetentionLocked(context.Background(), data)
		if err != nil || physical.Bytes != actual.MeasuredBytes {
			t.Fatalf("managed result does not equal actual final files: physical=%+v collector=%+v err=%v", physical, actual, err)
		}
		for i := 0; i < 100; i++ {
			clean, err := counter.CompleteManagedLocked(context.Background(), base, func(context.Context) (StoreBudgetResult, error) {
				calls++
				return StoreBudgetResult{}, errors.New("unexpected repeated floor census")
			})
			if err != nil || clean.Collected || clean.UpperBytes != physical.Bytes || calls != 1 {
				t.Fatalf("clean managed completion %d recounted: %+v calls=%d err=%v", i, clean, calls, err)
			}
		}
		if err := m.requireRetentionHistoryFromLocked("session", 1); !errors.Is(err, ErrHistoryExpired) {
			t.Fatal("managed expiry lost its rewind frontier:", err)
		}
		if err := m.requireRetentionHistoryFromLocked("session", 2); err != nil {
			t.Fatal("managed expiry blocked retained suffix:", err)
		}
	})
	assertFileBytes(t, protectedPath, protectedBytes)
	assertFileBytes(t, filepath.Join(m.home, "old.bin"), oldLive)
	if _, err := m.Undo(context.Background(), newest.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, filepath.Join(m.home, "new.bin"), []byte("new-before"))
	if _, err := m.Redo(context.Background(), newest.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, filepath.Join(m.home, "new.bin"), []byte("new-after"))
}

func TestManagedRetentionFloorShrinksBelowBaseAndReplansFreshAudit(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	m, err := Open(home, data)
	if errors.Is(err, ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	noise := func(seed int64, size int) []byte {
		data := make([]byte, size)
		if _, err := rand.New(rand.NewSource(seed)).Read(data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	old := recordParallelStoreFileChange(t, m, "old", "session", "old.bin", 1, noise(1, 96<<10), noise(2, 96<<10))
	newest := recordParallelStoreFileChange(t, m, "new", "session", "new.bin", 2, noise(3, 32<<10), noise(4, 32<<10))
	const base int64 = 128 << 10
	retentionTransaction(t, m, func() {
		audit, err := AuditStoreRetentionLocked(context.Background(), data)
		if err != nil {
			t.Fatal(err)
		}
		floor, err := PlanStoreRetention(audit.Inventory, 0)
		if !errors.Is(err, ErrStoreBudget) || floor.ProtectedBytes >= base {
			t.Fatalf("unexpected initial fixture floor: %+v %v", floor, err)
		}
		// The initial floor exceeds base by exactly one byte. Removing the old
		// record saves more metadata than its permanent frontier adds; the new
		// after snapshot is still rooted by latest throughout both passes.
		protectedPath, contents := managedRetentionFixedFile(t, data, base+1-floor.ProtectedBytes)
		audit, err = AuditStoreRetentionLocked(context.Background(), data)
		if err != nil {
			t.Fatal(err)
		}
		floor, err = PlanStoreRetention(audit.Inventory, base)
		if !errors.Is(err, ErrStoreBudget) || floor.ProtectedBytes != base+1 {
			t.Fatalf("fixture must cross the policy threshold: %+v %v", floor, err)
		}
		first, err := PlanStoreRetention(audit.Inventory, base+floor.ProtectedBytes)
		if err != nil || len(first.Expired) != 1 || first.Expired[0].ID != old.ID {
			t.Fatalf("fixture must initially keep newest with grace: %+v %v", first, err)
		}
		result, err := EnforceManagedStoreBudgetLocked(context.Background(), data, base)
		if err != nil || !result.FullCensusComplete || result.ProtectedBytes > base || result.MeasuredBytes > base || len(result.Expired) != 2 || result.Expired[0].ID != old.ID || result.Expired[1].ID != newest.ID {
			t.Fatalf("stale floor allowed total above fresh effective limit: %+v %v", result, err)
		}
		physical, err := MeasureStoreRetentionLocked(context.Background(), data)
		if err != nil || physical.Bytes != result.MeasuredBytes {
			t.Fatalf("fresh final usage differs: %+v %+v %v", physical, result, err)
		}
		assertFileBytes(t, protectedPath, contents)
		if err := m.requireRetentionHistoryFromLocked("session", 2); !errors.Is(err, ErrHistoryExpired) {
			t.Fatal("second expiry lost its permanent frontier:", err)
		}
		if _, err := m.git(context.Background(), "cat-file", "-e", newest.After); err != nil {
			t.Fatal("managed expiry deleted latest's exact protected graph:", err)
		}
	})
}

func TestManagedRetentionPhysicalBelowBaseSkipsGitAndResetsProof(t *testing.T) {
	m, data, _, _ := retentionFixture(t)
	retentionTransaction(t, m, func() {
		original := []byte("synthetic unknown metadata kept byte for byte")
		if err := os.WriteFile(m.meta, original, 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", t.TempDir())
		result, err := EnforceManagedStoreBudgetLocked(context.Background(), data, DefaultStoreBudgetBytes)
		if err != nil || !result.PhysicalComplete || result.FullCensusComplete || result.ProtectedBytes != 0 || len(result.Expired) != 0 || result.MeasuredBytes <= 0 || result.MeasuredBytes > DefaultStoreBudgetBytes {
			t.Fatalf("under-base check incorrectly required or reused a floor proof: %+v %v", result, err)
		}
		assertFileBytes(t, m.meta, original)
	})
}

func TestManagedRetentionProtectedOnlyFloorFitsExplicitException(t *testing.T) {
	data := t.TempDir()
	gate, err := NewStoreGate(data)
	if err != nil {
		t.Fatal(err)
	}
	protectedPath, contents := managedRetentionFixedFile(t, data, 4096)
	withUsageCounterGate(t, gate, func() {
		// No understood repository exists. Full conservative classification
		// still proves every physical byte fixed, without treating it as history.
		result, err := EnforceManagedStoreBudgetLocked(context.Background(), data, 100)
		if err != nil || !result.FullCensusComplete || result.MeasuredBytes != 4096 || result.ProtectedBytes != 4096 || len(result.Expired) != 0 || result.ReclaimedBytes != 0 {
			t.Fatalf("protected-only store was repeatedly rejected: %+v %v", result, err)
		}
		assertFileBytes(t, protectedPath, contents)
	})
}
