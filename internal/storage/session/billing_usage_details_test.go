package session

import (
	"context"
	"testing"
	"time"
)

func TestBillingRequestContextSurvivesDeleteAndOldJournalUpgrade(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	sess := mustCreateUsageSession(t, s, "/request-context-journal")
	ctx := context.Background()
	for i, u := range []UsageRecord{
		{ContextSystem: 3, ContextTool: 9},
		{ContextUser: 5}, // A known request with zero tool-role input.
		{},               // Historical request-shape coverage is unavailable.
	} {
		u.SessionID, u.CallSeq, u.Input, u.Output = sess.ID, i+1, 100, 20
		u.PriceSnapshot = billingSnapshot(2)
		if err := s.AppendUsage(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate the old journal schema. The upgrade must recover these two
	// fields from surviving usage, preserving counters and immutable prices.
	for _, col := range []string{"ctx_tool_tokens", "has_context_estimate"} {
		if _, err := s.db.Exec(`ALTER TABLE billing_usage DROP COLUMN ` + col); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`DELETE FROM billing_meta WHERE name=?`, billingContextCursor); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(sess.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := collectBilling(t, s, sess.ID, time.Time{})
	if len(got) != 3 || !got[0].ContextEstimateKnown || got[0].ContextTool != 9 ||
		!got[1].ContextEstimateKnown || got[1].ContextTool != 0 || got[2].ContextEstimateKnown {
		t.Fatalf("context estimate coverage lost: %+v", got)
	}
	for _, u := range got {
		if u.Input != 100 || u.Output != 20 || u.PriceSnapshot == nil || *u.PriceSnapshot.AmountUSD != 2 {
			t.Fatalf("upgrade rewrote accounted usage: %+v", u)
		}
	}
	var tokenRows []UsageRecord
	if err := s.VisitTokenUsage(ctx, sess.ID, func(u UsageRecord) { tokenRows = append(tokenRows, u) }); err != nil {
		t.Fatal(err)
	}
	if len(tokenRows) != len(got) {
		t.Fatal("token projection lost journal calls")
	}
	for i, u := range tokenRows {
		if u.PriceSnapshot != nil || u.Input != got[i].Input || u.Output != got[i].Output ||
			u.ContextTool != got[i].ContextTool || u.ContextEstimateKnown != got[i].ContextEstimateKnown {
			t.Fatalf("token projection differs from journal: %+v", u)
		}
	}
}

func TestLegacyAggregateMigrationDoesNotInventPartialCallsOrRepeat(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	legacy := mustCreateUsageSession(t, s, "/pre-ledger")
	partial := mustCreateUsageSession(t, s, "/partial-ledger")
	if err := s.AppendUsage(context.Background(), UsageRecord{SessionID: partial.ID, Input: 70, Output: 5, Source: "main"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{legacy.ID, partial.ID} {
		if _, err := s.db.Exec(`UPDATE sessions SET token_in=100,token_out=10 WHERE id=?`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`DELETE FROM billing_meta WHERE name=?`, billingLegacyMigration); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := collectBilling(t, s, "", time.Time{})
	if len(got) != 2 {
		t.Fatalf("migration invented/dropped calls: %+v", got)
	}
	legacyUsage, err := s.ReadUsage(context.Background(), legacy.ID)
	if err != nil || len(legacyUsage) != 1 || legacyUsage[0].Source != "legacy" || legacyUsage[0].Input != 100 || legacyUsage[0].Output != 10 {
		t.Fatalf("missing legacy aggregate: %+v err=%v", legacyUsage, err)
	}
	var gaps []LegacyUsageGap
	if err := s.VisitLegacyUsageGaps(context.Background(), "", func(g LegacyUsageGap) { gaps = append(gaps, g) }); err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 1 || gaps[0].SessionID != partial.ID || gaps[0].Input != 30 || gaps[0].Output != 5 {
		t.Fatalf("partial gap counted as usage: %+v", gaps)
	}
	if err := s.Delete(legacy.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BackfillBilling(context.Background(), func(u UsageRecord) PriceSnapshot { return *billingSnapshot(3) }); err != nil {
		t.Fatal(err)
	}
	got = collectBilling(t, s, "", time.Time{})
	if len(got) != 2 {
		t.Fatalf("migration repeated after delete/reopen: %+v", got)
	}
	for _, u := range got {
		if u.PriceSnapshot == nil || *u.PriceSnapshot.AmountUSD != 3 {
			t.Fatalf("deleted raw legacy did not freeze: %+v", u)
		}
	}
}

func TestLatestMainUsageBoundsHistoricalModelAndExcludesHelpers(t *testing.T) {
	s := openTestStore(t)
	sess := mustCreateUsageSession(t, s, "/latest-model")
	at := time.Date(2020, 1, 2, 12, 0, 0, 0, time.UTC)
	for i, u := range []UsageRecord{
		{Model: "main-old", Source: "model", CreatedAt: at},
		{Model: "compact", Source: "compact", CreatedAt: at.Add(time.Second)},
		{Model: "main-new", Source: "main", CreatedAt: at.Add(3 * time.Second)},
	} {
		u.SessionID, u.CallSeq = sess.ID, i+1
		if err := s.AppendUsage(context.Background(), u); err != nil {
			t.Fatal(err)
		}
	}
	got, found, err := s.LatestMainUsage(context.Background(), sess.ID, at.Add(2*time.Second))
	if err != nil || !found || got.Model != "main-old" {
		t.Fatalf("historical identity changed: %+v found=%v err=%v", got, found, err)
	}
	got, found, err = s.LatestMainUsage(context.Background(), sess.ID, time.Time{})
	if err != nil || !found || got.Model != "main-new" {
		t.Fatalf("latest identity missing: %+v found=%v err=%v", got, found, err)
	}
}
