package fx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestRefreshImportsOtherInstanceWithoutFetch(t *testing.T) {
	var calls atomic.Int32
	client := syntheticNBPClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode([]any{syntheticTable("2026-01-02", 4)})
	})
	dir := portableTestDir(t)
	writer, reader := testCache(t, dir, client), testCache(t, dir, client)
	if err := writer.EnsureDays(context.Background(), []string{"2026-01-04"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := reader.Lookup("2026-01-04", "PLN"); ok {
		t.Fatal("memory lookup implicitly read another instance's database")
	}
	if err := reader.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireRate(t, reader, "2026-01-04", "PLN", "2026-01-02", 4)
	if calls.Load() != 1 {
		t.Fatalf("local refresh issued a network request: calls=%d", calls.Load())
	}
}

func TestRefreshDoesNotWaitForBusyFetch(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	fetchClient := syntheticNBPClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
			_ = json.NewEncoder(w).Encode([]any{syntheticTable("2026-05-04", 8)})
		case <-r.Context().Done():
		}
	})
	writerClient := syntheticNBPClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{syntheticTable("2026-01-02", 4)})
	})
	dir := portableTestDir(t)
	fetcher, writer := testCache(t, dir, fetchClient), testCache(t, dir, writerClient)
	fetchCtx, cancelFetch := context.WithCancel(context.Background())
	defer cancelFetch()
	fetchDone := make(chan error, 1)
	go func() { fetchDone <- fetcher.EnsureDays(fetchCtx, []string{"2026-05-04"}) }()
	<-entered
	if err := writer.EnsureDays(context.Background(), []string{"2026-01-04"}); err != nil {
		t.Fatal(err)
	}
	refreshCtx, cancelRefresh := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancelRefresh()
	refreshDone := make(chan error, 1)
	go func() { refreshDone <- fetcher.Refresh(refreshCtx) }()
	select {
	case err := <-refreshDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-refreshCtx.Done():
		t.Fatal("local refresh waited for the unfinished HTTP request")
	}
	requireRate(t, fetcher, "2026-01-04", "PLN", "2026-01-02", 4)
	close(release)
	if err := <-fetchDone; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("refresh started another fetch: calls=%d", calls.Load())
	}
}

func TestRefreshSkipsBusyCommitMutexAndHonorsCancellation(t *testing.T) {
	client := syntheticNBPClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{syntheticTable("2026-01-02", 4)})
	})
	cache := testCache(t, portableTestDir(t), client)
	if err := cache.EnsureDays(context.Background(), []string{"2026-01-04"}); err != nil {
		t.Fatal(err)
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- cache.Refresh(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("busy snapshot should be preserved without error: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("refresh waited for a busy commit mutex")
	}
	canceledCtx, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if err := cache.Refresh(canceledCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled refresh while busy returned %v", err)
	}
	lookedUp := make(chan bool, 1)
	go func() {
		rate, ok := cache.Lookup("2026-01-04", "PLN")
		lookedUp <- ok && rate.Multiplier == 4 && rate.Date == "2026-01-02"
	}()
	select {
	case ok := <-lookedUp:
		if !ok {
			t.Fatal("lookup lost the committed in-memory snapshot")
		}
	case <-ctx.Done():
		t.Fatal("lookup waited for the commit mutex")
	}
}

func TestRefreshSQLBudgetAndClosedCache(t *testing.T) {
	cache := testCache(t, portableTestDir(t), nil)
	// Occupy the sole SQL connection. A refresh must time out locally rather
	// than stall a status read indefinitely while waiting for the SQL pool.
	conn, err := cache.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	err = cache.Refresh(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		conn.Close()
		t.Fatalf("SQL refresh did not honor its bounded context: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		conn.Close()
		t.Fatalf("SQL refresh exceeded its 150ms budget substantially: %v", elapsed)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cache.Refresh(context.Background()); err == nil {
		t.Fatal("refresh accepted a closed cache")
	}
}
