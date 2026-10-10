package session

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"
)

func billingSnapshot(amount float64) *PriceSnapshot {
	return &PriceSnapshot{State: "manual", AmountUSD: &amount, Source: "manual", InputPerMillion: 2,
		CachedInputPerMillion: 1, OutputPerMillion: 4, CacheKnown: true, PriceDate: "2020-02-04", UsageDay: "2020-02-03"}
}

func collectBilling(t *testing.T, s *Store, sessionID string, since time.Time) []UsageRecord {
	t.Helper()
	var out []UsageRecord
	if err := s.VisitBilling(context.Background(), sessionID, since, func(u UsageRecord) { out = append(out, u) }); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestBillingPriceSurvivesDeleteReopenAndRepeatedBackfill(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	sess := mustCreateUsageSession(t, s, "/billing-persist")
	price := billingSnapshot(5.5)
	want := *price
	wantAmount := *price.AmountUSD
	want.AmountUSD = &wantAmount
	u := UsageRecord{SessionID: sess.ID, Provider: "priced", ProviderType: "openai", EndpointHost: "api.example.test", Model: "model",
		Input: 1000000, Output: 1000000, CachedInput: 500000, HasCachedInput: true, PriceSnapshot: price,
		Source: "worker", CreatedAt: time.Date(2020, 2, 3, 8, 0, 0, 0, time.UTC)}
	if err := s.AppendUsage(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	*price.AmountUSD = 99
	rows, err := s.ReadUsage(context.Background(), sess.ID)
	if err != nil || len(rows) != 1 || !reflect.DeepEqual(rows[0].PriceSnapshot, &want) {
		t.Fatalf("read snapshot=%+v err=%v", rows, err)
	}
	var projected *PriceSnapshot
	if err := s.VisitUsageForStats(context.Background(), sess.ID, func(u UsageRecord) { projected = u.PriceSnapshot }); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(projected, &want) {
		t.Fatalf("stats projected a different quote: %+v", projected)
	}
	cleanup, err := s.DeleteRows(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.ReadUsage(context.Background(), sess.ID); err != nil || len(rows) != 0 {
		t.Fatalf("deleted usage=%+v err=%v", rows, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BackfillBilling(context.Background(), func(UsageRecord) PriceSnapshot { t.Fatal("existing frozen quote was repriced"); return PriceSnapshot{} }); err != nil {
		t.Fatal(err)
	}
	got := collectBilling(t, s, sess.ID, time.Time{})
	if len(got) != 1 || !reflect.DeepEqual(got[0].PriceSnapshot, &want) || got[0].Source != "worker" || got[0].Input != u.Input || got[0].Model != u.Model || got[0].SessionID != sess.ID {
		t.Fatalf("journal changed after deletion/reopen: %+v", got)
	}
}

func TestRawBillingMigrationSurvivesDeletionBeforePricingAndUsesBoundedBatches(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	sess := mustCreateUsageSession(t, s, "/billing-legacy")
	created := time.Date(2019, 2, 3, 22, 30, 0, 0, time.UTC)
	const count = billingBackfillBatch*2 + 3
	// Simulate a database created before the journal existed. Writing directly
	// avoids AppendUsage's new atomic raw-journal insertion.
	for seq := 1; seq <= count; seq++ {
		if _, err := s.db.Exec(`INSERT INTO session_usage(session_id,call_seq,provider,model,input_tokens,output_tokens,source,created_at)
			VALUES(?,?,?,?,?,?,?,?)`, sess.ID, seq, "legacy-provider", "legacy-model", seq, seq/2, "legacy", created.UnixNano()); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	before := collectBilling(t, s, sess.ID, time.Time{})
	if len(before) != count || before[0].PriceSnapshot != nil {
		t.Fatalf("raw migration=%d, first=%+v", len(before), before)
	}
	cleanup, err := s.DeleteRows(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	s.db.SetMaxOpenConns(1)
	quotes := 0
	if err := s.BackfillBilling(context.Background(), func(u UsageRecord) PriceSnapshot {
		quotes++
		var rows int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM billing_usage`).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows != count || u.PriceSnapshot != nil {
			t.Fatalf("backfill callback=%+v rows=%d", u, rows)
		}
		return *billingSnapshot(float64(u.Input))
	}); err != nil {
		t.Fatal(err)
	}
	if quotes != count {
		t.Fatalf("quotes=%d want=%d", quotes, count)
	}
	for _, u := range collectBilling(t, s, "", time.Time{}) {
		if u.PriceSnapshot == nil || !u.PriceSnapshot.Legacy || *u.PriceSnapshot.AmountUSD != float64(u.Input) || u.PriceSnapshot.UsageDay != created.In(time.Local).Format("2006-01-02") {
			t.Fatalf("deleted legacy record lost its frozen quote/day: %+v", u)
		}
	}
	if err := s.BackfillBilling(context.Background(), func(UsageRecord) PriceSnapshot { t.Fatal("backfill ran again"); return PriceSnapshot{} }); err != nil {
		t.Fatal(err)
	}
}

func TestBillingAppendIsAtomicOnJournalFailure(t *testing.T) {
	s := openTestStore(t)
	sess := mustCreateUsageSession(t, s, "/billing-atomic")
	ctx := context.Background()
	if _, err := s.db.Exec(`CREATE TRIGGER fail_billing_insert BEFORE INSERT ON billing_usage BEGIN SELECT RAISE(ABORT,'billing fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendUsage(ctx, UsageRecord{SessionID: sess.ID, Input: 10, PriceSnapshot: billingSnapshot(1)}); err == nil {
		t.Fatal("journal failure accepted usage")
	}
	if rows, err := s.ReadUsage(ctx, sess.ID); err != nil || len(rows) != 0 {
		t.Fatalf("half-committed usage=%+v err=%v", rows, err)
	}
	if got := collectBilling(t, s, "", time.Time{}); len(got) != 0 {
		t.Fatalf("half-committed journal=%+v", got)
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_billing_insert`); err != nil {
		t.Fatal(err)
	}
	bad := billingSnapshot(math.NaN())
	if err := s.AppendUsage(ctx, UsageRecord{SessionID: sess.ID, Input: 10, PriceSnapshot: bad}); err == nil {
		t.Fatal("nonfinite snapshot accepted")
	}
	if err := s.AppendUsage(ctx, UsageRecord{SessionID: sess.ID, Input: 10, PriceSnapshot: billingSnapshot(1)}); err != nil {
		t.Fatal(err)
	}
	rows := collectBilling(t, s, sess.ID, time.Time{})
	if len(rows) != 1 || rows[0].CallSeq != 1 {
		t.Fatalf("failed writes consumed/reused an identity incorrectly: %+v", rows)
	}
}

func TestBillingParallelCallsKeepSequenceAndNormalizedCounters(t *testing.T) {
	s := openTestStore(t)
	s.db.SetMaxOpenConns(4)
	sess := mustCreateUsageSession(t, s, "/billing-parallel")
	const count = 24
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- s.AppendUsage(context.Background(), UsageRecord{SessionID: sess.ID, Input: int64(i + 1), Output: 10,
				CachedInput: 999, Reasoning: 999, HasCachedInput: true, HasReasoning: true, PriceSnapshot: billingSnapshot(float64(i))})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.ReadUsage(context.Background(), sess.ID)
	if err != nil || len(rows) != count {
		t.Fatalf("usage len=%d err=%v", len(rows), err)
	}
	for i, u := range rows {
		if u.CallSeq != i+1 || u.CachedInput != u.Input || u.Reasoning != u.Output || u.PriceSnapshot == nil || *u.PriceSnapshot.AmountUSD != float64(u.Input-1) {
			t.Fatalf("parallel row=%+v", u)
		}
	}
	if got := collectBilling(t, s, "", time.Time{}); len(got) != count {
		t.Fatalf("journal len=%d", len(got))
	}
}

func TestBillingSequenceCannotReuseDeletedSessionCall(t *testing.T) {
	s := openTestStore(t)
	sess := mustCreateUsageSession(t, s, "/billing-recreate")
	ctx := context.Background()
	if err := s.AppendUsage(ctx, UsageRecord{SessionID: sess.ID, Input: 10, PriceSnapshot: billingSnapshot(1)}); err != nil {
		t.Fatal(err)
	}
	cleanup, err := s.DeleteRows(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureSession(sess.ID, "/billing-recreate", "model"); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendUsage(ctx, UsageRecord{SessionID: sess.ID, Input: 20, PriceSnapshot: billingSnapshot(2)}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendUsage(ctx, UsageRecord{SessionID: sess.ID, CallSeq: 1, Input: 30, PriceSnapshot: billingSnapshot(3)}); err == nil {
		t.Fatal("explicit sequence reused a deleted accounting record")
	}
	usage, err := s.ReadUsage(ctx, sess.ID)
	if err != nil || len(usage) != 1 || usage[0].CallSeq != 2 {
		t.Fatalf("recreated usage=%+v err=%v", usage, err)
	}
	journal := collectBilling(t, s, sess.ID, time.Time{})
	if len(journal) != 2 || *journal[0].PriceSnapshot.AmountUSD != 1 || *journal[1].PriceSnapshot.AmountUSD != 2 {
		t.Fatalf("accounting identities overwritten: %+v", journal)
	}
}

func TestVisitBillingFiltersAndPreservesNilLegacyQuote(t *testing.T) {
	s := openTestStore(t)
	first := mustCreateUsageSession(t, s, "/billing-filter-first")
	second := mustCreateUsageSession(t, s, "/billing-filter-second")
	base := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	for i, sid := range []string{first.ID, second.ID, first.ID} {
		if err := s.AppendUsage(context.Background(), UsageRecord{SessionID: sid, Input: int64(i + 1), CreatedAt: base.Add(time.Duration(i) * time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	if got := collectBilling(t, s, "", time.Time{}); len(got) != 3 || got[0].PriceSnapshot != nil {
		t.Fatalf("nil legacy journal=%+v", got)
	}
	if got := collectBilling(t, s, first.ID, base.Add(time.Hour)); len(got) != 1 || got[0].Input != 3 {
		t.Fatalf("session/since filter=%+v", got)
	}
	if rows, err := s.ReadUsage(context.Background(), first.ID); err != nil || len(rows) != 2 || rows[0].PriceSnapshot != nil {
		t.Fatalf("legacy nil semantics=%+v err=%v", rows, err)
	}
}

func TestBackfillPriceProjectsToSurvivingUsageAndKeepsCapturedDay(t *testing.T) {
	s := openTestStore(t)
	sess := mustCreateUsageSession(t, s, "/billing-projection")
	ctx := context.Background()
	if err := s.AppendUsage(ctx, UsageRecord{SessionID: sess.ID, Input: 100, CreatedAt: time.Date(2020, 2, 3, 0, 30, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	// Simulate a capture made in the previous local time zone. Later quoting
	// must use this saved day even when the application is moved elsewhere.
	if _, err := s.db.Exec(`UPDATE billing_usage SET usage_day='2020-02-02' WHERE session_id=?`, sess.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.BackfillBilling(ctx, func(UsageRecord) PriceSnapshot {
		return PriceSnapshot{State: "unknown", PriceDate: "2020-02-04", UsageDay: "2099-01-01"}
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ReadUsage(ctx, sess.ID)
	if err != nil || len(rows) != 1 || rows[0].PriceSnapshot == nil || rows[0].PriceSnapshot.State != "unknown" || rows[0].PriceSnapshot.AmountUSD != nil || !rows[0].PriceSnapshot.Legacy || rows[0].PriceSnapshot.UsageDay != "2020-02-02" {
		t.Fatalf("backfilled usage=%+v err=%v", rows, err)
	}
	var price *PriceSnapshot
	if err := s.VisitUsageForStats(ctx, sess.ID, func(u UsageRecord) { price = u.PriceSnapshot }); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(price, rows[0].PriceSnapshot) {
		t.Fatalf("stats price=%+v want=%+v", price, rows[0].PriceSnapshot)
	}
}

func TestBillingBackfillCancellationResumesWithoutRepricingCommittedBatch(t *testing.T) {
	s := openTestStore(t)
	s.db.SetMaxOpenConns(1)
	sess := mustCreateUsageSession(t, s, "/billing-cancel")
	const count = billingBackfillBatch + 3
	for i := 0; i < count; i++ {
		if err := s.AppendUsage(context.Background(), UsageRecord{SessionID: sess.ID, Input: 1}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	quotes := 0
	err := s.BackfillBilling(ctx, func(UsageRecord) PriceSnapshot {
		quotes++
		if quotes == billingBackfillBatch+1 {
			cancel()
		}
		return *billingSnapshot(1)
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled backfill=%v", err)
	}
	var priced int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM billing_usage WHERE price_snapshot IS NOT NULL`).Scan(&priced); err != nil {
		t.Fatal(err)
	}
	if priced != billingBackfillBatch {
		t.Fatalf("partial transaction committed %d quotes, want %d", priced, billingBackfillBatch)
	}
	retried := 0
	if err := s.BackfillBilling(context.Background(), func(UsageRecord) PriceSnapshot { retried++; return *billingSnapshot(2) }); err != nil {
		t.Fatal(err)
	}
	if retried != 3 {
		t.Fatalf("resume requoted %d calls, want 3", retried)
	}
	rows := collectBilling(t, s, sess.ID, time.Time{})
	for i, u := range rows {
		want := float64(1)
		if i >= billingBackfillBatch {
			want = 2
		}
		if u.PriceSnapshot == nil || *u.PriceSnapshot.AmountUSD != want {
			t.Fatalf("resumed price %d changed: %+v", i, u)
		}
	}
}

func TestPreserveLegacyUsageImmediatelyJournalsResumeBeforeDeletion(t *testing.T) {
	s := openTestStore(t)
	sess := mustCreateUsageSession(t, s, "/billing-legacy-resume")
	ctx := context.Background()
	if _, err := s.db.Exec(`UPDATE sessions SET token_in=1000, token_out=100 WHERE id=?`, sess.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := s.PreserveLegacyUsage(ctx, sess.ID); err != nil {
			t.Fatal(err)
		}
	}
	raw := collectBilling(t, s, sess.ID, time.Time{})
	if len(raw) != 1 || raw[0].Source != "legacy" || raw[0].Input != 1000 || raw[0].Output != 100 || raw[0].PriceSnapshot != nil {
		t.Fatalf("resume did not immediately capture raw aggregate: %+v", raw)
	}
	cleanup, err := s.DeleteRows(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if err := s.BackfillBilling(ctx, func(UsageRecord) PriceSnapshot { return *billingSnapshot(3) }); err != nil {
		t.Fatal(err)
	}
	priced := collectBilling(t, s, sess.ID, time.Time{})
	if len(priced) != 1 || priced[0].PriceSnapshot == nil || !priced[0].PriceSnapshot.Legacy || *priced[0].PriceSnapshot.AmountUSD != 3 {
		t.Fatalf("deletion before restart erased legacy billing: %+v", priced)
	}
}
