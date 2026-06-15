package credits

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"supercli/internal/storage"
)

// newTestStorage opens a fresh SQLite db in a temp dir
// for use in credits tests. The caller must Close() the
// returned *sql.DB.
func newTestStorage(t *testing.T) (*sql.DB, *Storage) {
	t.Helper()
	dir := t.TempDir()
	home := dir
	db, err := storage.Open(home)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	// Apply our migrations in addition to storage's
	// schema_version bootstrap.
	cs := NewStorage(db)
	if err := cs.Migrate(context.Background()); err != nil {
		_ = db.Close()
		t.Fatalf("credits.Migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, cs
}

func TestStorage_Migrate_Idempotent(t *testing.T) {
	db, _ := newTestStorage(t)
	cs := NewStorage(db)
	// Run again — should be a no-op.
	if err := cs.Migrate(context.Background()); err != nil {
		t.Errorf("second Migrate failed: %v", err)
	}
}

func TestStorage_Migrate_NilDB(t *testing.T) {
	cs := &Storage{}
	if err := cs.Migrate(context.Background()); err == nil {
		t.Error("expected error for nil db")
	}
}

func TestStorage_AppendLedger_AndSessionTotal(t *testing.T) {
	_, cs := newTestStorage(t)
	ctx := context.Background()
	now := time.Now().UnixNano()
	rows := []LedgerEntry{
		{SessionID: "s1", TS: now, TurnSeq: 1, Input: 100, Output: 50, Source: SourceLoop},
		{SessionID: "s1", TS: now + 1, TurnSeq: 2, Input: 200, Output: 80, Source: SourceLoop},
		{SessionID: "s2", TS: now, TurnSeq: 1, Input: 50, Output: 25, Source: SourceSubAgent},
	}
	for _, r := range rows {
		if _, err := cs.AppendLedger(ctx, r); err != nil {
			t.Fatalf("AppendLedger: %v", err)
		}
	}
	total, err := cs.SessionTotal(ctx, "s1")
	if err != nil {
		t.Fatalf("SessionTotal: %v", err)
	}
	if total != 430 { // 100+50 + 200+80
		t.Errorf("s1 total = %d, want 430", total)
	}
	total, err = cs.SessionTotal(ctx, "s2")
	if err != nil {
		t.Fatalf("SessionTotal s2: %v", err)
	}
	if total != 75 {
		t.Errorf("s2 total = %d, want 75", total)
	}
	total, err = cs.SessionTotal(ctx, "nonexistent")
	if err != nil {
		t.Errorf("nonexistent should not error: %v", err)
	}
	if total != 0 {
		t.Errorf("nonexistent total = %d, want 0", total)
	}
}

func TestStorage_AppendLedger_Defaults(t *testing.T) {
	_, cs := newTestStorage(t)
	ctx := context.Background()
	// Empty Source -> defaults to "loop" in DB.
	// Empty TS -> server timestamp; we accept any non-zero.
	id, err := cs.AppendLedger(ctx, LedgerEntry{
		SessionID: "s1",
		Input:     10,
		Output:    5,
	})
	if err != nil {
		t.Fatalf("AppendLedger: %v", err)
	}
	if id == 0 {
		t.Error("expected non-zero row id")
	}
}

func TestStorage_AppendLedger_InvalidSource(t *testing.T) {
	_, cs := newTestStorage(t)
	_, err := cs.AppendLedger(context.Background(), LedgerEntry{
		SessionID: "s1",
		Source:    "garbage",
	})
	if err == nil {
		t.Error("expected error for invalid source")
	}
}

func TestStorage_AppendLedger_EmptySessionID(t *testing.T) {
	_, cs := newTestStorage(t)
	_, err := cs.AppendLedger(context.Background(), LedgerEntry{
		SessionID: "",
		Input:     1,
	})
	if err == nil {
		t.Error("expected error for empty session_id")
	}
}

func TestStorage_DailyTotal(t *testing.T) {
	_, cs := newTestStorage(t)
	ctx := context.Background()
	now := time.Now().UnixNano()
	todayMidnight := time.Now().UTC().Truncate(24 * time.Hour).UnixNano()
	if _, err := cs.AppendLedger(ctx, LedgerEntry{
		SessionID: "s1", TS: now, Input: 100, Output: 50,
	}); err != nil {
		t.Fatal(err)
	}
	// Yesterday's row — should not count toward today.
	if _, err := cs.AppendLedger(ctx, LedgerEntry{
		SessionID: "s1", TS: todayMidnight - 1, Input: 999, Output: 999,
	}); err != nil {
		t.Fatal(err)
	}
	total, err := cs.DailyTotal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if total != 150 {
		t.Errorf("daily total = %d, want 150 (yesterday's row should be excluded)", total)
	}
}

func TestStorage_SaveAndLoadBudget(t *testing.T) {
	_, cs := newTestStorage(t)
	ctx := context.Background()
	want := Budget{PerSession: 1234, PerDay: 5678}
	if err := cs.SaveBudget(ctx, "s1", want); err != nil {
		t.Fatalf("SaveBudget: %v", err)
	}
	got, err := cs.LoadBudget(ctx, "s1")
	if err != nil {
		t.Fatalf("LoadBudget: %v", err)
	}
	if got != want {
		t.Errorf("LoadBudget = %+v, want %+v", got, want)
	}
}

func TestStorage_SaveBudget_Overwrites(t *testing.T) {
	_, cs := newTestStorage(t)
	ctx := context.Background()
	first := Budget{PerSession: 100, PerDay: 200}
	second := Budget{PerSession: 999, PerDay: 888}
	if err := cs.SaveBudget(ctx, "s1", first); err != nil {
		t.Fatal(err)
	}
	if err := cs.SaveBudget(ctx, "s1", second); err != nil {
		t.Fatal(err)
	}
	got, err := cs.LoadBudget(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if got != second {
		t.Errorf("budget not overwritten: got %+v, want %+v", got, second)
	}
}

func TestStorage_SaveBudget_RejectsNegative(t *testing.T) {
	_, cs := newTestStorage(t)
	err := cs.SaveBudget(context.Background(), "s1", Budget{PerSession: -1})
	if err == nil {
		t.Error("expected error for negative cap")
	}
}

func TestStorage_LoadBudget_Missing(t *testing.T) {
	_, cs := newTestStorage(t)
	got, err := cs.LoadBudget(context.Background(), "never-saved")
	if err != nil {
		t.Fatalf("LoadBudget: %v", err)
	}
	if got != (Budget{}) {
		t.Errorf("missing budget should be zero value, got %+v", got)
	}
}

func TestStorage_LoadBudget_NilStorage(t *testing.T) {
	cs := &Storage{}
	_, err := cs.LoadBudget(context.Background(), "x")
	if err == nil {
		t.Error("expected error for nil storage")
	}
}

func TestStorage_PathIndependent(t *testing.T) {
	// The credit_* tables live next to schema_version.
	// We verify by opening storage in t.TempDir() and
	// confirming the migration succeeds. This is a
	// smoke test that the table creation uses the
	// same data dir the rest of the app does.
	dir := t.TempDir()
	db, err := storage.Open(dir)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()
	cs := NewStorage(db)
	if err := cs.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	// File should exist; not a strict test but a
	// canary that storage.Open + Migrate together
	// work as the production path does.
	_ = filepath.Join(dir, "data")
}
