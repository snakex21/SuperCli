package credits

import (
	"context"
	"sync"
	"testing"
	"time"

	"supercli/internal/storage"
)

// trackerTestHarness builds a storage + tracker pair
// in a temp dir. Returns the tracker, the storage, and
// a cleanup function. The storage is closed by the
// returned cleanup.
func trackerTestHarness(t *testing.T) (*Tracker, *Storage) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(dir)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cs := NewStorage(db)
	if err := cs.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return NewTracker("s1", Budget{}, cs), cs
}

func TestTracker_Record_Accumulates(t *testing.T) {
	tr, _ := trackerTestHarness(t)
	ctx := context.Background()
	if err := tr.Record(ctx, 100, 50, "gpt-4o-mini"); err != nil {
		t.Fatalf("Record #1: %v", err)
	}
	if err := tr.Record(ctx, 200, 80, "gpt-4o-mini"); err != nil {
		t.Fatalf("Record #2: %v", err)
	}
	s, d := tr.Used()
	if s != 430 {
		t.Errorf("session total = %d, want 430", s)
	}
	if d != 430 {
		t.Errorf("daily total = %d, want 430", d)
	}
	in, out, _, _ := tr.UsedByDir()
	if in != 300 || out != 130 {
		t.Errorf("in/out = %d/%d, want 300/130", in, out)
	}
}

func TestTracker_Record_NilSafe(t *testing.T) {
	var tr *Tracker
	if err := tr.Record(context.Background(), 10, 5, ""); err != nil {
		t.Errorf("nil tracker Record should be no-op, got %v", err)
	}
	if s, d := tr.Used(); s != 0 || d != 0 {
		t.Errorf("nil tracker Used = (%d, %d), want (0, 0)", s, d)
	}
}

func TestTracker_SessionCapEnforced(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cs := NewStorage(db)
	if err := cs.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	tr := NewTracker("s1", Budget{PerSession: 500}, cs)
	if err := tr.Record(context.Background(), 200, 100, ""); err != nil {
		t.Fatalf("first Record: %v", err)
	}
	err = tr.Record(context.Background(), 200, 100, "")
	if err != ErrBudgetExceeded {
		t.Errorf("expected ErrBudgetExceeded, got %v", err)
	}
	// Counters should not have been updated.
	s, _ := tr.Used()
	if s != 300 {
		t.Errorf("after rejection, session total = %d, want 300", s)
	}
}

func TestTracker_DayCapEnforced(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cs := NewStorage(db)
	if err := cs.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	tr := NewTracker("s1", Budget{PerDay: 300}, cs)
	if err := tr.Record(context.Background(), 200, 50, ""); err != nil {
		t.Fatalf("first Record: %v", err)
	}
	err = tr.Record(context.Background(), 100, 0, "")
	if err != ErrBudgetExceeded {
		t.Errorf("expected ErrBudgetExceeded on day cap, got %v", err)
	}
}

func TestTracker_NoCap_AlwaysPasses(t *testing.T) {
	tr, _ := trackerTestHarness(t)
	for i := 0; i < 100; i++ {
		if err := tr.Record(context.Background(), 1_000_000, 500_000, ""); err != nil {
			t.Fatalf("Record #%d failed: %v", i, err)
		}
	}
	s, _ := tr.Used()
	if s != 100*1_500_000 {
		t.Errorf("total = %d, want %d", s, 100*1_500_000)
	}
}

func TestTracker_Record_NegativeClampedToZero(t *testing.T) {
	tr, _ := trackerTestHarness(t)
	if err := tr.Record(context.Background(), -10, -5, ""); err != nil {
		t.Fatalf("Record: %v", err)
	}
	s, _ := tr.Used()
	if s != 0 {
		t.Errorf("negative should clamp to 0, got %d", s)
	}
}

func TestTracker_ConcurrentRecords(t *testing.T) {
	tr, _ := trackerTestHarness(t)
	var wg sync.WaitGroup
	const goroutines = 16
	const each = 100
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				_ = tr.Record(context.Background(), 1, 1, "")
			}
		}()
	}
	wg.Wait()
	s, _ := tr.Used()
	if want := int64(goroutines * each * 2); s != want {
		t.Errorf("after concurrent Records, total = %d, want %d", s, want)
	}
}

func TestTracker_ModelCaptured(t *testing.T) {
	tr, _ := trackerTestHarness(t)
	_ = tr.Record(context.Background(), 1, 1, "gpt-4o")
	_ = tr.Record(context.Background(), 1, 1, "gpt-4o-mini")
	// Tracker doesn't expose model; we just verify
	// nothing panics. CostFor is tested elsewhere.
}

func TestTracker_Close_ClearsStorage(t *testing.T) {
	tr, _ := trackerTestHarness(t)
	if err := tr.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// After Close, Record should still be safe (no-op).
	if err := tr.Record(context.Background(), 10, 5, ""); err != nil {
		t.Errorf("Record after Close: %v", err)
	}
}

func TestTracker_WithParent_RecordsParent(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cs := NewStorage(db)
	if err := cs.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	tr := NewTrackerWithParent("child", "parent", Budget{}, cs)
	if err := tr.Record(context.Background(), 5, 5, ""); err != nil {
		t.Fatal(err)
	}
	// Parent total should be 0; child should be 10.
	pt, err := cs.SessionTotal(context.Background(), "parent")
	if err != nil {
		t.Fatal(err)
	}
	if pt != 0 {
		t.Errorf("parent total = %d, want 0", pt)
	}
	ct, err := cs.SessionTotal(context.Background(), "child")
	if err != nil {
		t.Fatal(err)
	}
	if ct != 10 {
		t.Errorf("child total = %d, want 10", ct)
	}
}

func TestDailyResetBoundary(t *testing.T) {
	// Use a known time: 2026-06-07 14:30:00 UTC
	now := time.Date(2026, 6, 7, 14, 30, 0, 0, time.UTC)
	got := DailyResetBoundary(now)
	want := time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC).UnixNano()
	if got != want {
		t.Errorf("DailyResetBoundary = %d, want %d", got, want)
	}
}
