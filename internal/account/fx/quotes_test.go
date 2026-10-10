package fx

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func syntheticExtraTable(table, day string, values map[string]float64) map[string]any {
	rates := make([]map[string]any, 0, len(values))
	for code, mid := range values {
		rates = append(rates, map[string]any{"code": code, "mid": mid})
	}
	return map[string]any{"table": table, "effectiveDate": day, "rates": rates}
}

func TestOfficialCurrencyCatalog(t *testing.T) {
	codes := SupportedCurrencies()
	if len(codes) != 148 {
		t.Fatalf("official real A/B currency count=%d", len(codes))
	}
	seen := make(map[string]bool)
	for _, code := range codes {
		if !validCurrencyCode(code) || seen[code] {
			t.Fatalf("invalid/duplicate code %q", code)
		}
		seen[code] = true
	}
	for _, code := range []string{"AUD", "CNY", "HKD", "INR", "KRW", "AED", "VND", "XCG", "ZWG"} {
		if !seen[code] {
			t.Fatalf("official currency missing: %s", code)
		}
	}
	for _, code := range []string{"XDR", "XAU", "BTC"} {
		if _, err := NormalizeCurrency(code); err == nil {
			t.Fatalf("non-currency accepted: %s", code)
		}
	}
}

func TestAdditionalCurrencyPreservesOldSnapshotAndPortableRestart(t *testing.T) {
	var calls atomic.Int32
	client := syntheticNBPClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if r.Header.Get("User-Agent") != "SuperCli/1.0" {
			t.Error("missing application User-Agent")
		}
		table := syntheticTable("2026-01-02", 4)
		if n > 1 {
			// The old USD=4 snapshot must win over the newly fetched USD=40.
			table = syntheticTable("2026-01-02", 40)
			table["rates"] = append(table["rates"].([]map[string]any), map[string]any{"code": "AUD", "mid": 2}, map[string]any{"code": "NZD", "mid": 1})
		}
		_ = json.NewEncoder(w).Encode([]any{table})
	})
	dir := portableTestDir(t)
	c := testCache(t, dir, client)
	ctx := context.Background()
	if err := c.EnsureDays(ctx, []string{"2026-01-04"}); err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureCurrency(ctx, []string{"2026-01-04"}, "AUD"); err != nil {
		t.Fatal(err)
	}
	requireRate(t, c, "2026-01-04", "AUD", "2026-01-02", 2)
	requireRate(t, c, "2026-01-04", "PLN", "2026-01-02", 4)
	requireRate(t, c, "2026-01-04", "EUR", "2026-01-02", .8)
	if err := c.EnsureCurrency(ctx, []string{"2026-01-04"}, "NZD"); err != nil || calls.Load() != 2 {
		t.Fatalf("shared A table refetched: %d %v", calls.Load(), err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(portableTestDir(t), "moved")
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	c = testCache(t, moved, nil)
	if err := c.EnsureCurrency(ctx, []string{"2026-01-04"}, "AUD"); err != nil {
		t.Fatal(err)
	}
	requireRate(t, c, "2026-01-04", "AUD", "2026-01-02", 2)
}

func TestWeeklyTableBPreservesBothPublicationDatesAndSharesTable(t *testing.T) {
	var calls atomic.Int32
	client := syntheticNBPClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if strings.Contains(r.URL.Path, "/tables/a/") {
			_ = json.NewEncoder(w).Encode([]any{syntheticTable("2026-01-09", 4)})
			return
		}
		// A preceding Tuesday holiday publication and a newer future B table.
		_ = json.NewEncoder(w).Encode([]any{syntheticExtraTable("B", "2026-01-06", map[string]float64{"AED": 2, "VND": .001}), syntheticExtraTable("B", "2026-01-14", map[string]float64{"AED": 4, "VND": .002})})
	})
	c := testCache(t, portableTestDir(t), client)
	if err := c.EnsureCurrency(context.Background(), []string{"2026-01-13", "2026-01-15"}, "AED"); err != nil {
		t.Fatal(err)
	}
	r, ok := c.Lookup("2026-01-13", "AED")
	if !ok || r.Multiplier != 2 || r.Date != "2026-01-06" || r.USDDate != "2026-01-09" || r.Source != quoteBSource {
		t.Fatalf("weekly cross quote=%+v %t", r, ok)
	}
	if err := c.EnsureCurrency(context.Background(), []string{"2026-01-13"}, "VND"); err != nil || calls.Load() != 2 {
		t.Fatalf("shared B refetched %d %v", calls.Load(), err)
	}
}

func TestTableBCanBeNewerThanFrozenUSDAnchor(t *testing.T) {
	c := testCache(t, portableTestDir(t), syntheticNBPClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/tables/a/") {
			_ = json.NewEncoder(w).Encode([]any{syntheticTable("2026-01-06", 4)})
			return
		}
		_ = json.NewEncoder(w).Encode([]any{syntheticExtraTable("B", "2026-01-07", map[string]float64{"AED": 2})})
	}))
	if err := c.EnsureCurrency(context.Background(), []string{"2026-01-08"}, "AED"); err != nil {
		t.Fatal(err)
	}
	r, ok := c.Lookup("2026-01-08", "AED")
	if !ok || r.Date != "2026-01-07" || r.USDDate != "2026-01-06" {
		t.Fatalf("B must be latest to usage day, independently of frozen USD: %+v %v", r, ok)
	}
}

func TestCurrencyMovingBetweenTablesFallsBackOnlyWhenMissing(t *testing.T) {
	var calls atomic.Int32
	c := testCache(t, portableTestDir(t), syntheticNBPClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if strings.Contains(r.URL.Path, "/tables/b/") {
			_ = json.NewEncoder(w).Encode([]any{syntheticExtraTable("B", "2026-01-07", map[string]float64{"AED": 2})})
			return
		}
		table := syntheticTable("2026-01-09", 4)
		if n > 1 {
			table["rates"] = append(table["rates"].([]map[string]any), map[string]any{"code": "RUB", "mid": .05})
		}
		_ = json.NewEncoder(w).Encode([]any{table})
	}))
	if err := c.EnsureCurrency(context.Background(), []string{"2026-01-09"}, "RUB"); err != nil {
		t.Fatal(err)
	}
	requireRate(t, c, "2026-01-09", "RUB", "2026-01-09", 80)
	if calls.Load() != 3 {
		t.Fatalf("base+A/B bounded fallback calls=%d", calls.Load())
	}
}

func TestQuoteServerFailureDoesNotRetryOtherTable(t *testing.T) {
	var calls atomic.Int32
	c := testCache(t, portableTestDir(t), syntheticNBPClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if strings.Contains(r.URL.Path, "/tables/a/") {
			_ = json.NewEncoder(w).Encode([]any{syntheticTable("2026-01-09", 4)})
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	err := c.EnsureCurrency(context.Background(), []string{"2026-01-09"}, "AED")
	var status *nbpStatusError
	if !errors.As(err, &status) || status.code != 403 || calls.Load() != 2 {
		t.Fatalf("server failure should not cause table retry: %v calls=%d", err, calls.Load())
	}
}

func TestQuoteTableNotFoundFallsBackToOtherOfficialTable(t *testing.T) {
	var calls atomic.Int32
	c := testCache(t, portableTestDir(t), syntheticNBPClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if strings.Contains(r.URL.Path, "/tables/b/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		table := syntheticTable("2026-01-09", 4)
		if n > 1 {
			table["rates"] = append(table["rates"].([]map[string]any), map[string]any{"code": "RUB", "mid": .05})
		}
		_ = json.NewEncoder(w).Encode([]any{table})
	}))
	if err := c.EnsureCurrency(context.Background(), []string{"2026-01-09"}, "RUB"); err != nil {
		t.Fatal(err)
	}
	requireRate(t, c, "2026-01-09", "RUB", "2026-01-09", 80)
	if calls.Load() != 3 {
		t.Fatalf("missing table should have one bounded fallback: %d", calls.Load())
	}
}

func TestAdditionalQuoteDatabaseFirstWinnerAndRefresh(t *testing.T) {
	dir := portableTestDir(t)
	client := syntheticNBPClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{syntheticTable("2026-01-09", 4)})
	})
	first, second := testCache(t, dir, client), testCache(t, dir, client)
	if err := first.EnsureDays(context.Background(), []string{"2026-01-09"}); err != nil {
		t.Fatal(err)
	}
	if err := second.EnsureDays(context.Background(), []string{"2026-01-09"}); err != nil {
		t.Fatal(err)
	}
	commit := func(c *Cache, multiplier float64) error {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.commitQuotes(context.Background(), map[string]map[string]Rate{"2026-01-09": {"AUD": {Multiplier: multiplier, Date: "2026-01-09", USDDate: "2026-01-09", Source: rateSource}}})
	}
	if err := commit(second, 2); err != nil {
		t.Fatal(err)
	}
	if err := commit(first, 20); err != nil {
		t.Fatal(err)
	}
	requireRate(t, first, "2026-01-09", "AUD", "2026-01-09", 2)
	reopened := testCache(t, dir, nil)
	requireRate(t, reopened, "2026-01-09", "AUD", "2026-01-09", 2)
	if err := first.EnsureCurrency(context.Background(), []string{"2026-01-09"}, "AUD"); err != nil {
		t.Fatal(err)
	}
}

func TestQuoteCancellationDoesNotCancelOtherWaiter(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	c := testCache(t, portableTestDir(t), syntheticNBPClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/tables/a/") {
			_ = json.NewEncoder(w).Encode([]any{syntheticTable("2026-01-09", 4)})
			return
		}
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_ = json.NewEncoder(w).Encode([]any{syntheticExtraTable("B", "2026-01-07", map[string]float64{"AED": 2})})
	}))
	ctx, cancel := context.WithCancel(context.Background())
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- c.EnsureCurrency(ctx, []string{"2026-01-09"}, "AED") }()
	<-entered
	// Register the second waiter under the same lock, avoiding time-based polling.
	c.mu.Lock()
	flight := c.flight
	flight.waiters++
	c.mu.Unlock()
	go func() {
		<-flight.done
		c.leaveFlight(flight)
		second <- c.EnsureCurrency(context.Background(), []string{"2026-01-09"}, "AED")
	}()
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("first waiter=%v", err)
	}
	once.Do(func() { close(release) })
	if err := <-second; err != nil {
		t.Fatal(err)
	}
}

func TestTableBRangesRespectNinetyThreeDayLimit(t *testing.T) {
	days := []string{"2026-01-01", "2026-03-20", "2026-03-21", "2026-06-01"}
	spans := rangesForDaysWithLookback(days, bLookbackDays)
	seen := make(map[string]bool)
	for _, span := range spans {
		if int(span.end.Sub(span.start)/(24*time.Hour))+1 > 93 {
			t.Fatalf("oversized NBP range: %+v", span)
		}
		for _, day := range span.days {
			if seen[day] {
				t.Fatalf("duplicate range owner %s", day)
			}
			seen[day] = true
		}
	}
	if len(seen) != len(days) || len(spans) != 2 {
		t.Fatalf("range coverage %+v", spans)
	}
}

func TestValidateQuoteRejectsInvalidFrozenSources(t *testing.T) {
	if err := ValidateQuote("2026-01-09", "AED", "2025-12-30", "2026-01-09", quoteBSource, 2); err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct {
		day, pub, usd, source string
		rate                  float64
	}{
		{"2026-01-09", "2026-01-10", "2026-01-09", quoteBSource, 2},
		{"2026-01-09", "2025-12-25", "2026-01-09", quoteBSource, 2},
		{"2026-01-09", "2026-01-07", "2025-12-30", quoteBSource, 2},
		{"2026-01-09", "2026-01-07", "2026-01-09", rateSource, 2},
		{"2026-01-09", "2026-01-09", "2026-01-09", "invented", 2},
		{"2026-01-09", "2026-01-09", "2026-01-09", rateSource, math.Inf(1)},
	} {
		if err := ValidateQuote(change.day, "AED", change.pub, change.usd, change.source, change.rate); err == nil {
			t.Fatalf("invalid quote accepted %+v", change)
		}
	}
}
