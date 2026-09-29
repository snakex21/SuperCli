package webgui

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"supercli/internal/account/credits"
	"supercli/internal/storage/session"
)

func TestStatsDailyTotalsObserveAnotherProcess(t *testing.T) {
	eng, store, sess, dir := statsFixture(t)
	ctx := context.Background()
	before, err := eng.stats(ctx, sess.ID)
	if err != nil || before.DailyToken != 0 {
		t.Fatalf("initial total = %d, error = %v", before.DailyToken, err)
	}
	sharedDB, sharedLedger := eng.creditDB, eng.creditLedger
	db, err := openDataDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	writer := credits.NewStorage(db)
	if err := writer.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	// An earlier day's ledger entry must not enter today's total.
	if _, err := writer.AppendLedger(ctx, credits.LedgerEntry{SessionID: "tui", TS: midnight.Add(-time.Hour).UnixNano(), Input: 1000, Output: 1000}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		if _, err := writer.AppendLedger(ctx, credits.LedgerEntry{SessionID: "tui", TS: now.UnixNano(), Input: 30, Output: 5}); err != nil {
			t.Fatal(err)
		}
		// Web usage is separate from the CLI ledger and must be counted once.
		if err := store.AppendUsage(ctx, session.UsageRecord{SessionID: sess.ID, Input: 10, Output: 2, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		got, err := eng.stats(ctx, sess.ID)
		if err != nil || got.DailyToken != int64(i*47) {
			t.Fatalf("refresh %d: total = %d, want %d; error = %v", i, got.DailyToken, i*47, err)
		}
		if eng.creditDB != sharedDB || eng.creditLedger != sharedLedger {
			t.Fatal("statistics reopened the credit ledger")
		}
	}
}

func TestEngineCreditStorageConcurrentReuseAndClose(t *testing.T) {
	eng, _, _, _ := statsFixture(t)
	if eng.creditDB != nil || eng.creditLedger != nil {
		t.Fatal("ledger eagerly initialized")
	}
	const callers = 24
	stores := make([]*credits.Storage, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range stores {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stores[i], errs[i] = eng.creditStorage(context.Background())
		}()
	}
	wg.Wait()
	for i, ledger := range stores {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if ledger == nil || ledger != stores[0] {
			t.Fatal("concurrent callers received different ledger handles")
		}
	}
	db := eng.creditDB
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Ping(); err == nil {
		t.Fatal("ledger connection survived engine shutdown")
	}
	if _, err := eng.creditStorage(context.Background()); err == nil {
		t.Fatal("closed engine reopened the ledger")
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestEngineCreditStorageCanceledInitializationCanRetry(t *testing.T) {
	eng, _, _, _ := statsFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := eng.creditStorage(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled initialization: %v", err)
	}
	if eng.creditDB != nil || eng.creditLedger != nil {
		t.Fatal("canceled request retained a connection")
	}
	ledger, err := eng.creditStorage(context.Background())
	if err != nil || ledger == nil {
		t.Fatalf("retry: %v", err)
	}
}
