package usagecost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type historyRatesTransport func(*http.Request) (*http.Response, error)

func (f historyRatesTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet || req.URL.Scheme != "https" || req.URL.Host != "api.nbp.pl" {
		return nil, fmt.Errorf("unexpected exchange request: %s %s", req.Method, req.URL)
	}
	return f(req)
}

func portableHistoryRatesDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".tmp", "usagecost-fx-tests"))
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

// Explicitly synthetic NBP table values, used only by this transport fixture.
func historyRatesResponse(req *http.Request, publication string) *http.Response {
	body := fmt.Sprintf(`[{"table":"A","effectiveDate":%q,"rates":[
		{"code":"USD","mid":4},{"code":"EUR","mid":5},
		{"code":"GBP","mid":6},{"code":"CHF","mid":4.5},
		{"code":"JPY","mid":0.03},{"code":"CAD","mid":3},
		{"code":"CZK","mid":0.2},{"code":"NOK","mid":0.4},
		{"code":"SEK","mid":0.5}]}]`, publication)
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(body)), Request: req}
}

func historyRatesGate(t *testing.T) (<-chan struct{}, func()) {
	t.Helper()
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	return gate, release
}

func awaitHistoryRatesSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal("exchange fixture did not reach its completion boundary")
	}
}

func awaitHistoryRatesResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("exchange completion wait did not return")
		return nil
	}
}

func TestHistoryRatesWaitDoesNotStartWork(t *testing.T) {
	dir := filepath.Join(portableHistoryRatesDir(t), "not-opened")
	var calls atomic.Int32
	h := NewHistoryRatesWithClient(dir, &http.Client{Transport: historyRatesTransport(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("network should not be reached")
	})})
	defer h.Close()
	if pending, generation := h.State(); pending || generation != 0 {
		t.Fatalf("idle state = %v/%d", pending, generation)
	}
	if err := h.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) || calls.Load() != 0 {
		t.Fatalf("completion-only API opened storage or network: stat=%v calls=%d", err, calls.Load())
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.Wait(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("idle canceled wait: %v", err)
	}
	var nilHistory *HistoryRates
	if nilHistory.Pending() || nilHistory.Generation() != 0 || nilHistory.Wait(context.Background()) != nil {
		t.Fatal("nil completion API should have no pending work")
	}
	if _, err := nilHistory.Cache(); err == nil {
		t.Fatal("nil Cache accepted")
	}
	if err := nilHistory.Ensure(context.Background(), []string{"2026-01-04"}); err == nil {
		t.Fatal("nil Ensure accepted")
	}
}

func TestHistoryRatesWarmDeduplicatesAndWaitCancellationIsIndependent(t *testing.T) {
	entered := make(chan struct{})
	gate, release := historyRatesGate(t)
	var calls atomic.Int32
	h := NewHistoryRatesWithClient(portableHistoryRatesDir(t), &http.Client{Transport: historyRatesTransport(func(req *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-gate:
			return historyRatesResponse(req, "2026-01-02"), nil
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	})})
	defer h.Close()
	h.Warm("2026-01-04", "2026-01-04")
	awaitHistoryRatesSignal(t, entered)
	for i := 0; i < 20; i++ {
		h.Warm("2026-01-04")
	}
	h.mu.Lock()
	batches, days := h.pending, len(h.warming)
	h.mu.Unlock()
	if batches != 1 || days != 1 || h.Generation() != 1 {
		t.Fatalf("duplicate Warm accepted extra work: batches=%d days=%d generation=%d", batches, days, h.Generation())
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.Wait(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter: %v", err)
	}
	short, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	if err := h.Wait(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("bounded waiter: %v", err)
	}
	if !h.Pending() || calls.Load() != 1 {
		t.Fatalf("wait cancellation changed background work: pending=%v calls=%d", h.Pending(), calls.Load())
	}
	completed := make(chan error, 1)
	go func() { completed <- h.Wait(context.Background()) }()
	release()
	if err := awaitHistoryRatesResult(t, completed); err != nil {
		t.Fatal(err)
	}
	cache, err := h.Cache()
	if err != nil {
		t.Fatal(err)
	}
	if rate, ok := cache.Lookup("2026-01-04", "PLN"); !ok || rate.Multiplier != 4 {
		t.Fatalf("shared fetch failed after a waiter canceled: rate=%+v ok=%v", rate, ok)
	}
	h.Warm("2026-01-04", "2026-01-04")
	if pending, generation := h.State(); pending || generation != 1 || calls.Load() != 1 {
		t.Fatalf("cached Warm changed cycle or fetched: pending=%v generation=%d calls=%d", pending, generation, calls.Load())
	}
}

func TestHistoryRatesWarmSkipsDaysAlreadyInCache(t *testing.T) {
	var calls atomic.Int32
	h := NewHistoryRatesWithClient(portableHistoryRatesDir(t), &http.Client{Transport: historyRatesTransport(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		return historyRatesResponse(req, "2026-01-02"), nil
	})})
	defer h.Close()
	if err := h.Ensure(context.Background(), []string{"2026-01-04"}); err != nil {
		t.Fatal(err)
	}
	h.Warm("2026-01-04")
	if err := h.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if pending, generation := h.State(); pending || generation != 0 || calls.Load() != 1 {
		t.Fatalf("cached day accepted a Warm: pending=%v generation=%d calls=%d", pending, generation, calls.Load())
	}
}

func TestHistoryRatesWaitIncludesAllWorkAcceptedInCurrentCycle(t *testing.T) {
	first, second, third := make(chan struct{}), make(chan struct{}), make(chan struct{})
	gates := make([]<-chan struct{}, 3)
	releases := make([]func(), 3)
	for i := range gates {
		gates[i], releases[i] = historyRatesGate(t)
	}
	var calls atomic.Int32
	h := NewHistoryRatesWithClient(portableHistoryRatesDir(t), &http.Client{Transport: historyRatesTransport(func(req *http.Request) (*http.Response, error) {
		i := int(calls.Add(1)) - 1
		if i >= len(gates) {
			return nil, errors.New("unexpected duplicate exchange fetch")
		}
		close([]chan struct{}{first, second, third}[i])
		select {
		case <-gates[i]:
			return historyRatesResponse(req, []string{"2026-01-02", "2026-05-04", "2026-09-04"}[i]), nil
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	})})
	defer h.Close()
	h.Warm("2026-01-04")
	awaitHistoryRatesSignal(t, first)
	h.Warm("2026-05-04")
	if pending, generation := h.State(); !pending || generation != 1 {
		t.Fatalf("second batch did not join active cycle: %v/%d", pending, generation)
	}
	completed := make(chan error, 1)
	go func() { completed <- h.Wait(context.Background()) }()
	releases[0]()
	awaitHistoryRatesSignal(t, second)
	select {
	case err := <-completed:
		t.Fatalf("wait ended before second accepted batch: %v", err)
	default:
	}
	releases[1]()
	if err := awaitHistoryRatesResult(t, completed); err != nil {
		t.Fatal(err)
	}
	if pending, generation := h.State(); pending || generation != 1 {
		t.Fatalf("finished cycle state: %v/%d", pending, generation)
	}
	h.Warm("2026-09-04")
	awaitHistoryRatesSignal(t, third)
	if pending, generation := h.State(); !pending || generation != 2 {
		t.Fatalf("new work did not create a new generation: %v/%d", pending, generation)
	}
	releases[2]()
	if err := h.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryRatesFailureCompletesWithoutAutomaticRetry(t *testing.T) {
	var calls atomic.Int32
	h := NewHistoryRatesWithClient(portableHistoryRatesDir(t), &http.Client{Transport: historyRatesTransport(func(req *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("synthetic offline error")
		}
		return historyRatesResponse(req, "2026-01-02"), nil
	})})
	defer h.Close()
	h.Warm("2026-01-04")
	if err := h.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	cache, err := h.Cache()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Lookup("2026-01-04", "PLN"); ok {
		t.Fatal("failed fetch fabricated a cached rate")
	}
	if pending, generation := h.State(); pending || generation != 1 || calls.Load() != 1 {
		t.Fatalf("failed fetch did not finish once: %v/%d calls=%d", pending, generation, calls.Load())
	}
	if err := h.Wait(context.Background()); err != nil || calls.Load() != 1 {
		t.Fatalf("wait retried a failed fetch: %v calls=%d", err, calls.Load())
	}
	h.Warm("2026-01-04")
	if err := h.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if pending, generation := h.State(); pending || generation != 2 || calls.Load() != 2 {
		t.Fatalf("explicit retry did not create exactly one cycle: %v/%d calls=%d", pending, generation, calls.Load())
	}
}

func TestHistoryRatesCloseCancelsAndCompletesAcceptedWarm(t *testing.T) {
	entered, requestCanceled := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	h := NewHistoryRatesWithClient(portableHistoryRatesDir(t), &http.Client{Transport: historyRatesTransport(func(req *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-req.Context().Done()
		close(requestCanceled)
		return nil, req.Context().Err()
	})})
	defer h.Close()
	h.Warm("2026-01-04")
	awaitHistoryRatesSignal(t, entered)
	waited, closed := make(chan error, 1), make(chan error, 1)
	go func() { waited <- h.Wait(context.Background()) }()
	go func() { closed <- h.Close() }()
	if err := awaitHistoryRatesResult(t, closed); err != nil {
		t.Fatal(err)
	}
	if err := awaitHistoryRatesResult(t, waited); err != nil {
		t.Fatal(err)
	}
	awaitHistoryRatesSignal(t, requestCanceled)
	h.Warm("2026-05-04")
	if pending, generation := h.State(); pending || generation != 1 || calls.Load() != 1 {
		t.Fatalf("closed cache accepted work: %v/%d calls=%d", pending, generation, calls.Load())
	}
	if _, err := h.Cache(); err == nil {
		t.Fatal("closed Cache accepted")
	}
	if err := h.Ensure(context.Background(), []string{"2026-05-04"}); err == nil {
		t.Fatal("closed Ensure accepted")
	}
}
