package fx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func portableTestDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".tmp", "fx-tests"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "case-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

type rewriteTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (r rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || req.URL.Host != "api.nbp.pl" || req.Method != http.MethodGet {
		return nil, fmt.Errorf("unexpected FX request: %s %s", req.Method, req.URL)
	}
	copy := req.Clone(req.Context())
	copy.URL.Scheme, copy.URL.Host = r.target.Scheme, r.target.Host
	return r.base.RoundTrip(copy)
}

func syntheticNBPClient(t *testing.T, handler http.HandlerFunc) *http.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		server.CloseClientConnections()
		server.Close()
	})
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Transport: rewriteTransport{target: target, base: http.DefaultTransport}}
}

// These are explicitly synthetic table values, not published exchange rates.
func syntheticTable(day string, usd float64) map[string]any {
	mid := map[string]float64{"USD": usd, "EUR": 5, "GBP": 6, "CHF": 4.5, "JPY": .03, "CAD": 3, "CZK": .2, "NOK": .4, "SEK": .5}
	rates := make([]map[string]any, 0, len(mid))
	for code, value := range mid {
		rates = append(rates, map[string]any{"code": code, "mid": value})
	}
	return map[string]any{"table": "A", "effectiveDate": day, "rates": rates}
}

func testCache(t *testing.T, dir string, client *http.Client) *Cache {
	t.Helper()
	cache, err := NewWithClient(dir, client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Close() })
	return cache
}

func requireRate(t *testing.T, cache *Cache, day, currency, date string, multiplier float64) {
	t.Helper()
	rate, ok := cache.Lookup(day, currency)
	if !ok || rate.Date != date || rate.Source != rateSource || math.Abs(rate.Multiplier-multiplier) > 1e-12 {
		t.Fatalf("%s/%s rate=%+v ok=%v; want %s multiplier=%g", day, currency, rate, ok, date, multiplier)
	}
}

func TestNormalizeCurrencyAndUSDWithoutNetwork(t *testing.T) {
	for _, code := range SupportedCurrencies() {
		got, err := NormalizeCurrency(" " + strings.ToLower(code) + " ")
		if err != nil || got != code {
			t.Fatalf("normalize %q: %q %v", code, got, err)
		}
	}
	if code, err := NormalizeCurrency(""); err != nil || code != "USD" {
		t.Fatalf("empty preference: %q %v", code, err)
	}
	if _, err := NormalizeCurrency("BTC"); err == nil {
		t.Fatal("unsupported currency accepted")
	}
	copy := SupportedCurrencies()
	copy[0] = "modified"
	if SupportedCurrencies()[0] != "USD" {
		t.Fatal("mutable shared currency list")
	}
	var cache *Cache
	if rate, ok := cache.Lookup("2026-01-04", "USD"); !ok || rate.Multiplier != 1 {
		t.Fatalf("USD needs no cache/network: %+v %v", rate, ok)
	}
	if _, ok := cache.Lookup("2026-01-04", "EUR"); ok {
		t.Fatal("missing FX rate invented")
	}
}

func TestHistoricalRatesWeekendCrossAndPortableRestart(t *testing.T) {
	var calls atomic.Int32
	var usd atomic.Int64
	usd.Store(4)
	client := syntheticNBPClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode([]any{syntheticTable("2026-01-02", float64(usd.Load())), syntheticTable("2026-01-05", 8)})
	})
	dir := portableTestDir(t)
	cache := testCache(t, dir, client)
	if err := cache.EnsureDays(context.Background(), []string{"2026-01-04", "2026-01-02", "2026-01-05", "2026-01-04"}); err != nil {
		t.Fatal(err)
	}
	requireRate(t, cache, "2026-01-04", "PLN", "2026-01-02", 4)
	requireRate(t, cache, "2026-01-04", "EUR", "2026-01-02", .8)
	requireRate(t, cache, "2026-01-05", "JPY", "2026-01-05", 8/.03)
	usd.Store(40)
	if err := cache.EnsureDays(context.Background(), []string{"2026-01-04"}); err != nil || calls.Load() != 1 {
		t.Fatalf("cached day refetched: calls=%d err=%v", calls.Load(), err)
	}
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	// Move the app's data folder with its database; restart is fully offline.
	moved := filepath.Join(portableTestDir(t), "moved-app-data")
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	cache = testCache(t, moved, nil)
	if err := cache.EnsureDays(context.Background(), []string{"2026-01-04"}); err != nil {
		t.Fatal(err)
	}
	requireRate(t, cache, "2026-01-04", "PLN", "2026-01-02", 4)
}

func TestDayRolloverFetchesOnlyMissingDays(t *testing.T) {
	var calls atomic.Int32
	client := syntheticNBPClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			_ = json.NewEncoder(w).Encode([]any{syntheticTable("2026-01-02", 4)})
		} else {
			_ = json.NewEncoder(w).Encode([]any{syntheticTable("2026-01-02", 40), syntheticTable("2026-01-05", 8)})
		}
	})
	cache := testCache(t, portableTestDir(t), client)
	for _, days := range [][]string{{"2026-01-04"}, {"2026-01-04", "2026-01-05"}} {
		if err := cache.EnsureDays(context.Background(), days); err != nil {
			t.Fatal(err)
		}
	}
	requireRate(t, cache, "2026-01-04", "PLN", "2026-01-02", 4)
	requireRate(t, cache, "2026-01-05", "PLN", "2026-01-05", 8)
	if calls.Load() != 2 {
		t.Fatalf("rollover calls=%d", calls.Load())
	}
}

func TestAnnualHistoryUsesBoundedBatches(t *testing.T) {
	var calls atomic.Int32
	client := syntheticNBPClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 6 {
			http.Error(w, "bad path", 400)
			return
		}
		from, err1 := parseDay(parts[4])
		to, err2 := parseDay(parts[5])
		if err1 != nil || err2 != nil || to.Sub(from) > 92*24*time.Hour {
			http.Error(w, "range exceeds 93 days", 400)
			return
		}
		var tables []any
		for date := from; !date.After(to); date = date.AddDate(0, 0, 1) {
			if date.Weekday() != time.Saturday && date.Weekday() != time.Sunday {
				tables = append(tables, syntheticTable(date.Format(dateLayout), 4))
			}
		}
		_ = json.NewEncoder(w).Encode(tables)
	})
	cache := testCache(t, portableTestDir(t), client)
	start, _ := parseDay("2025-01-01")
	var days []string
	for i := 0; i < 365; i++ {
		days = append(days, start.AddDate(0, 0, i).Format(dateLayout))
	}
	if err := cache.EnsureDays(context.Background(), days); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 5 {
		t.Fatalf("365 usage days used %d requests, want 5 bounded batches", calls.Load())
	}
	for _, day := range days {
		rate, ok := cache.Lookup(day, "EUR")
		if !ok || rate.Date > day || rate.Multiplier != .8 {
			t.Fatalf("invalid annual day %s: %+v %v", day, rate, ok)
		}
	}
}

func TestErrorsAndFutureTablesDoNotFreezeFakeRates(t *testing.T) {
	for _, scenario := range []string{"offline", "zero", "future", "stale", "malformed"} {
		t.Run(scenario, func(t *testing.T) {
			client := syntheticNBPClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch scenario {
				case "offline":
					http.Error(w, "offline", 503)
				case "zero":
					_ = json.NewEncoder(w).Encode([]any{syntheticTable("2026-01-02", 0)})
				case "future":
					_ = json.NewEncoder(w).Encode([]any{syntheticTable("2026-01-05", 4)})
				case "stale":
					_ = json.NewEncoder(w).Encode([]any{syntheticTable("2025-12-26", 4)})
				case "malformed":
					_, _ = w.Write([]byte("not JSON"))
				}
			})
			cache := testCache(t, portableTestDir(t), client)
			if err := cache.EnsureDays(context.Background(), []string{"2026-01-04"}); err == nil {
				t.Fatal("invalid response accepted")
			}
			if _, ok := cache.Lookup("2026-01-04", "PLN"); ok {
				t.Fatal("failed fetch froze a fictional rate")
			}
			var rows int
			if err := cache.db.QueryRow("SELECT count(*) FROM currency_days").Scan(&rows); err != nil || rows != 0 {
				t.Fatalf("invalid rates persisted: rows=%d err=%v", rows, err)
			}
		})
	}
}

func TestPartialFetchFailureRetainsGoodDaysAndRetriesOnlyMissing(t *testing.T) {
	var calls atomic.Int32
	client := syntheticNBPClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			_ = json.NewEncoder(w).Encode([]any{syntheticTable("2026-01-02", 4)})
		case 2:
			http.Error(w, "temporarily offline", http.StatusServiceUnavailable)
		default:
			if !strings.Contains(r.URL.Path, "/2026-04-27/2026-05-04/") {
				http.Error(w, "refetched an already frozen day", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode([]any{syntheticTable("2026-05-04", 8)})
		}
	})
	cache := testCache(t, portableTestDir(t), client)
	days := []string{"2026-01-04", "2026-05-04"}
	if err := cache.EnsureDays(context.Background(), days); err == nil {
		t.Fatal("partial failure was hidden")
	}
	requireRate(t, cache, "2026-01-04", "PLN", "2026-01-02", 4)
	if _, ok := cache.Lookup("2026-05-04", "PLN"); ok {
		t.Fatal("failed day received another range's stale table")
	}
	if err := cache.EnsureDays(context.Background(), days); err != nil {
		t.Fatal(err)
	}
	requireRate(t, cache, "2026-05-04", "PLN", "2026-05-04", 8)
	if calls.Load() != 3 {
		t.Fatalf("retry fetched cached days: calls=%d", calls.Load())
	}
}

func TestConcurrentFetchDeduplicatedAndCallerCancellation(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	client := syntheticNBPClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
			_ = json.NewEncoder(w).Encode([]any{syntheticTable("2026-01-02", 4)})
		case <-r.Context().Done():
		}
	})
	cache := testCache(t, portableTestDir(t), client)
	first := make(chan error, 1)
	go func() { first <- cache.EnsureDays(context.Background(), []string{"2026-01-04"}) }()
	<-entered
	const others = 8
	var started, finished sync.WaitGroup
	started.Add(others)
	finished.Add(others)
	errorsFound := make(chan error, others)
	for i := 0; i < others; i++ {
		go func() {
			defer finished.Done()
			started.Done()
			errorsFound <- cache.EnsureDays(context.Background(), []string{"2026-01-04"})
		}()
	}
	started.Wait()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := cache.EnsureDays(ctx, []string{"2026-01-04"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled caller: %v", err)
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	finished.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("concurrent callers made %d requests", calls.Load())
	}
}

func TestLastCallerCancellationAbortsFetchAndAllowsRetry(t *testing.T) {
	entered, aborted := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	client := syntheticNBPClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
			<-r.Context().Done()
			close(aborted)
			return
		}
		_ = json.NewEncoder(w).Encode([]any{syntheticTable("2026-01-02", 4)})
	})
	cache := testCache(t, portableTestDir(t), client)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- cache.EnsureDays(ctx, []string{"2026-01-04"}) }()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel result: %v", err)
	}
	<-aborted
	if _, ok := cache.Lookup("2026-01-04", "PLN"); ok {
		t.Fatal("canceled operation persisted rates")
	}
	if err := cache.EnsureDays(context.Background(), []string{"2026-01-04"}); err != nil {
		t.Fatal(err)
	}
	requireRate(t, cache, "2026-01-04", "PLN", "2026-01-02", 4)
}

func TestSeparateInstancesImportCommittedDayWithoutRefetch(t *testing.T) {
	var calls atomic.Int32
	client := syntheticNBPClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode([]any{syntheticTable("2026-01-02", 4)})
	})
	dir := portableTestDir(t)
	first, second := testCache(t, dir, client), testCache(t, dir, client)
	if err := first.EnsureDays(context.Background(), []string{"2026-01-04"}); err != nil {
		t.Fatal(err)
	}
	if err := second.EnsureDays(context.Background(), []string{"2026-01-04"}); err != nil || calls.Load() != 1 {
		t.Fatalf("second instance refetched: calls=%d err=%v", calls.Load(), err)
	}
	requireRate(t, second, "2026-01-04", "PLN", "2026-01-02", 4)
}

func TestConcurrentInstancesPreserveFirstCommittedSnapshot(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	winnerClient := syntheticNBPClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{syntheticTable("2026-01-02", 4)})
	})
	staleClient := syntheticNBPClient(t, func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-release:
			_ = json.NewEncoder(w).Encode([]any{syntheticTable("2026-01-02", 40)})
		case <-r.Context().Done():
		}
	})
	dir := portableTestDir(t)
	winner, stale := testCache(t, dir, winnerClient), testCache(t, dir, staleClient)
	done := make(chan error, 1)
	go func() { done <- stale.EnsureDays(context.Background(), []string{"2026-01-04"}) }()
	<-entered
	if err := winner.EnsureDays(context.Background(), []string{"2026-01-04"}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	requireRate(t, winner, "2026-01-04", "PLN", "2026-01-02", 4)
	requireRate(t, stale, "2026-01-04", "PLN", "2026-01-02", 4)
	var rows int
	if err := winner.db.QueryRow("SELECT count(*) FROM currency_days").Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("unique frozen row: rows=%d err=%v", rows, err)
	}
}
