package llm

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeLimiterClock drives the limiter with a manual clock; retrySleep advances it.
func fakeLimiterClock(t *testing.T) (*sync.Mutex, *time.Time) {
	t.Helper()
	var mu sync.Mutex
	now := time.Unix(1_000_000, 0)
	savedNow, savedSleep := limiterNow, retrySleep
	limiterNow = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	retrySleep = func(ctx context.Context, d time.Duration) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		mu.Lock()
		now = now.Add(d)
		mu.Unlock()
		return nil
	}
	ResetRequestLimits()
	t.Cleanup(func() { limiterNow, retrySleep = savedNow, savedSleep; ResetRequestLimits() })
	return &mu, &now
}

func TestRequestLimitKeyNormalizes(t *testing.T) {
	if a, b := RequestLimitKey("https://API.Example.com/v1/"), RequestLimitKey("https://api.example.com/v1"); a != b || a != "https://api.example.com" {
		t.Fatalf("keys %q %q", a, b)
	}
}

func TestRequestLimiterWaitsForWindowAndNotifies(t *testing.T) {
	mu, now := fakeLimiterClock(t)
	SetDefaultRequestsPerMinute(2)
	var notices []string
	note := func(s string) { notices = append(notices, s) }
	start := limiterNow()
	for i := 0; i < 5; i++ {
		if err := WaitRequestSlot(context.Background(), "https://ai.example/v1", note); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	elapsed := now.Sub(start)
	mu.Unlock()
	// 2 per minute: starts at 0,0,60,60,120.
	if elapsed != 2*time.Minute {
		t.Fatalf("elapsed = %v, want 2m", elapsed)
	}
	if len(notices) != 2 || !strings.Contains(notices[0], "ai.example: request limit 2/min reached — waiting 1m0s") {
		t.Fatalf("notices = %v", notices)
	}
}

func TestRequestLimiterPerProviderOverrideAndOff(t *testing.T) {
	fakeLimiterClock(t)
	SetDefaultRequestsPerMinute(0)
	SetRequestsPerMinute("https://slow.example/v1", 1)
	if RequestsPerMinuteFor("https://other.example") != 0 || RequestsPerMinuteFor("https://slow.example") != 1 {
		t.Fatal("override must apply only to its endpoint")
	}
	for i := 0; i < 50; i++ {
		if err := WaitRequestSlot(context.Background(), "https://other.example", nil); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRequestLimiterSharedAcrossGoroutines(t *testing.T) {
	fakeLimiterClock(t)
	SetDefaultRequestsPerMinute(3)
	// Freeze the clock (sleep does not advance it) to count how many
	// starts get through inside one window.
	retrySleep = func(ctx context.Context, d time.Duration) error {
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	var passed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if WaitRequestSlot(ctx, "https://shared.example", nil) == nil {
				passed.Add(1)
			}
		}()
	}
	deadline := time.Now().Add(2 * time.Second)
	for passed.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	cancel()
	wg.Wait()
	if got := passed.Load(); got != 3 {
		t.Fatalf("passed = %d, want 3 (shared bucket)", got)
	}
}

func TestRequestLimiterCancelIsImmediate(t *testing.T) {
	ResetRequestLimits()
	t.Cleanup(ResetRequestLimits)
	SetDefaultRequestsPerMinute(1)
	ctx, cancel := context.WithCancel(context.Background())
	if err := WaitRequestSlot(ctx, "https://c.example", nil); err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(20*time.Millisecond, cancel)
	start := time.Now()
	if err := WaitRequestSlot(ctx, "https://c.example", nil); err == nil {
		t.Fatal("expected cancellation")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("cancel took %v", elapsed)
	}
}

func TestOpenAIProviderUsesRequestLimiter(t *testing.T) {
	mu, now := fakeLimiterClock(t)
	var calls int32
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		sseResponse(w, `{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
	})
	SetRequestsPerMinute(srv.URL, 1)
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "m"})
	start := limiterNow()
	var notices int
	for i := 0; i < 2; i++ {
		ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range drainDeltas(t, ch) {
			if d.Notice != "" {
				notices++
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 || now.Sub(start) != time.Minute || notices != 1 {
		t.Fatalf("calls=%d elapsed=%v notices=%d", calls, now.Sub(start), notices)
	}
}
