package webgui

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/account/credits"
	"supercli/internal/account/usagecost"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

func TestDailyTokensExcludeMultiDayLegacyAggregateButPreserveHistoryAndLedger(t *testing.T) {
	eng, store, sess, _ := statsFixture(t)
	ctx := context.Background()
	today := time.Now()
	startOfDay := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())
	// This synthetic old total spans a 30-day conversation resumed today.
	// Its updated-at timestamp must not assign all historical tokens to today.
	legacy := session.UsageRecord{SessionID: sess.ID, Source: "legacy", Model: "old-unknown-breakdown",
		Input: 100000, Output: 10000, CreatedAt: today}
	if err := store.AppendUsage(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUsage(ctx, session.UsageRecord{SessionID: sess.ID, Source: "main", Input: 70, Output: 5, CreatedAt: today}); err != nil {
		t.Fatal(err)
	}
	ledger, err := eng.creditStorage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.AppendLedger(ctx, credits.LedgerEntry{SessionID: sess.ID, Source: credits.SourceLoop,
		Input: 70, Output: 5, TS: today.Add(time.Millisecond).UnixNano()}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.AppendLedger(ctx, credits.LedgerEntry{SessionID: sess.ID, Source: credits.SourceLoop,
		Input: 10, Output: 2, TS: today.Add(-time.Millisecond).UnixNano()}); err != nil {
		t.Fatal(err)
	}
	if got := dailyTokenTotal(ctx, store, ledger, startOfDay); got != 87 {
		t.Fatalf("daily includes a multi-day aggregate or duplicate measured ledger: %d", got)
	}
	got, err := eng.stats(ctx, sess.ID)
	if err != nil || got.SessionToken != 110075 || got.DailyToken != 87 {
		t.Fatalf("session history lost or daily inflated: tokens=%d daily=%d err=%v", got.SessionToken, got.DailyToken, err)
	}
	history, err := eng.usage(ctx, "")
	if err != nil || history.Totals.Total != 110075 || history.Totals.Calls != 1 || history.Totals.LegacyRecords != 1 {
		t.Fatalf("legacy not retained in history: %+v err=%v", history, err)
	}
}

func TestCostRateRetrySkipsSyntheticLegacyDates(t *testing.T) {
	s := newTestServer(t, false)
	store, err := s.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(s.eng.Home(), "legacy", "")
	if err != nil {
		t.Fatal(err)
	}
	global, _ := config.FindTomlPaths(s.eng.DataDir(), s.eng.Home())
	tc := config.TomlConfig{CostCurrency: "PLN", ModelPrices: []config.ModelPriceConf{{Model: "legacy", InputCost: 1}}}
	if err := config.SaveToml(global, tc); err != nil {
		t.Fatal(err)
	}
	u := session.UsageRecord{SessionID: sess.ID, Source: "legacy", Model: "legacy", Input: 1000000,
		CreatedAt: time.Now()}
	price := usagecost.FreezeQuote(tc, u, true)
	u.PriceSnapshot = &price
	if err := store.AppendUsage(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	s.eng.costRates = usagecost.NewHistoryRatesWithClient(s.eng.DataDir(), &http.Client{Transport: costsTransport(func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, fmt.Errorf("legacy aggregate must not fetch a synthetic daily rate")
	})})
	for i := 0; i < 2; i++ {
		if err := s.eng.ensureCostRates(context.Background(), sess.ID); err != nil {
			t.Fatal(err)
		}
	}
	if requests.Load() != 0 || s.eng.costRates.Generation() != 0 || s.eng.costRates.Pending() {
		t.Fatal("irrecoverable aggregate accepted FX retries or warm work")
	}
	got, err := s.eng.costs(context.Background(), sess.ID)
	if err != nil || got.Total.Amount != nil || got.Total.BaseAmountUSD == nil || *got.Total.BaseAmountUSD != 1 ||
		got.Total.MissingFXCalls != 1 || got.Total.Source != "fx_missing" {
		t.Fatalf("legacy FX fabricated zero/current rate: %+v err=%v", got, err)
	}
}
