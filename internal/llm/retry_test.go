package llm

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// --- unit: Retry-After parsing and wait selection ---

func TestParseRetryAfter(t *testing.T) {
	h := http.Header{}
	if _, ok := parseRetryAfter(h); ok {
		t.Fatal("absent header should report ok=false")
	}
	h.Set("Retry-After", "2")
	if d, ok := parseRetryAfter(h); !ok || d != 2*time.Second {
		t.Fatalf("seconds form: d=%v ok=%v", d, ok)
	}
	h.Set("Retry-After", "0")
	if d, ok := parseRetryAfter(h); !ok || d != 0 {
		t.Fatalf("zero seconds: d=%v ok=%v", d, ok)
	}
	h.Set("Retry-After", "-5")
	if d, ok := parseRetryAfter(h); !ok || d != 0 {
		t.Fatalf("negative seconds clamps to 0: d=%v ok=%v", d, ok)
	}
	// HTTP-date form ~3s in the future (allow generous slack).
	h.Set("Retry-After", time.Now().Add(3*time.Second).UTC().Format(http.TimeFormat))
	if d, ok := parseRetryAfter(h); !ok || d <= 0 || d > 4*time.Second {
		t.Fatalf("date form: d=%v ok=%v", d, ok)
	}
	// Date in the past clamps to 0.
	h.Set("Retry-After", time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat))
	if d, ok := parseRetryAfter(h); !ok || d != 0 {
		t.Fatalf("past date clamps to 0: d=%v ok=%v", d, ok)
	}
	h.Set("Retry-After", "garbage")
	if _, ok := parseRetryAfter(h); ok {
		t.Fatal("garbage header should report ok=false")
	}
}

func TestRetryWait_HeaderWinsAndIsCapped(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "3")
	if w := retryWait(h, 1, rateLimitWaitBudget); w != 3*time.Second {
		t.Fatalf("header within budget: w=%v want 3s", w)
	}
	// A huge Retry-After must be clamped to the remaining budget —
	// this is the cap that keeps a 429 storm from hanging a turn.
	h.Set("Retry-After", "3600")
	if w := retryWait(h, 1, rateLimitWaitBudget); w != rateLimitWaitBudget {
		t.Fatalf("header over budget: w=%v want %v", w, rateLimitWaitBudget)
	}
	if w := retryWait(h, 1, 2*time.Second); w != 2*time.Second {
		t.Fatalf("partial budget: w=%v want 2s", w)
	}
	if w := retryWait(h, 1, 0); w != 0 {
		t.Fatalf("exhausted budget: w=%v want 0", w)
	}
}

func TestRetryWait_FallbackBackoffWithoutHeader(t *testing.T) {
	h := http.Header{}
	if w := retryWait(h, 1, rateLimitWaitBudget); w != 500*time.Millisecond {
		t.Fatalf("attempt 1: w=%v want 500ms", w)
	}
	if w := retryWait(h, 2, rateLimitWaitBudget); w != time.Second {
		t.Fatalf("attempt 2: w=%v want 1s", w)
	}
}

// --- integration: OpenAI provider over httptest ---

// openai429ThenOK returns 429 (with the given Retry-After header, if
// non-empty) for the first n requests, then a normal SSE completion.
func openai429ThenOK(t *testing.T, n int32, retryAfter string) (*OpenAIProvider, *int32) {
	t.Helper()
	var calls int32
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) <= n {
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			}
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"Too many requests"}`))
			return
		}
		sseResponse(w, `{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
	})
	p, err := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "rate-limited-model"})
	if err != nil {
		t.Fatal(err)
	}
	return p, &calls
}

func TestOpenAI_429_RetryAfterZeroSkipsBackoff(t *testing.T) {
	// Retry-After: 0 must be honoured: the retry fires immediately
	// instead of the default 500ms backoff. Elapsed time well under
	// 500ms proves the header (not the fallback) drove the wait.
	p, calls := openai429ThenOK(t, 1, "0")
	start := time.Now()
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ds := drainDeltas(t, ch)
	elapsed := time.Since(start)
	if got := atomic.LoadInt32(calls); got != 2 {
		t.Fatalf("calls = %d, want 2 (one 429 + one success)", got)
	}
	if elapsed >= 400*time.Millisecond {
		t.Errorf("elapsed = %v; Retry-After: 0 should skip the 500ms default backoff", elapsed)
	}
	var text strings.Builder
	var notices []string
	for _, d := range ds {
		text.WriteString(d.Content)
		if d.Notice != "" {
			notices = append(notices, d.Notice)
		}
		if d.Err != nil {
			t.Fatalf("unexpected stream error: %v", d.Err)
		}
	}
	if text.String() != "ok" {
		t.Errorf("content = %q, want ok", text.String())
	}
	// The user must SEE the wait: exactly one notice for the one retry.
	if len(notices) != 1 {
		t.Fatalf("notices = %v, want exactly 1", notices)
	}
	if !strings.Contains(notices[0], "rate limited") || !strings.Contains(notices[0], "retrying") {
		t.Errorf("notice = %q, want rate-limit retry wording", notices[0])
	}
	if !strings.Contains(notices[0], "rate-limited-model") {
		t.Errorf("notice = %q, should name the model", notices[0])
	}
}

func TestOpenAI_429_ExhaustedSaysSwitchModel(t *testing.T) {
	// All attempts 429 → the terminal error must say the model is
	// rate-limited and suggest switching, not just dump "http 429".
	p, calls := openai429ThenOK(t, 99, "0")
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ds := drainDeltas(t, ch)
	if got := atomic.LoadInt32(calls); got != 3 {
		t.Fatalf("calls = %d, want 3 (maxAttempts)", got)
	}
	last := ds[len(ds)-1]
	if last.Err == nil {
		t.Fatal("expected terminal error after exhausted retries")
	}
	msg := last.Err.Error()
	if !strings.Contains(msg, "http 429") {
		t.Errorf("err = %q, want http 429", msg)
	}
	if !strings.Contains(msg, "rate-limited") || !strings.Contains(msg, "/model") {
		t.Errorf("err = %q, want actionable rate-limit hint with /model", msg)
	}
	// Two retries → two notices before the terminal error.
	var notices int
	for _, d := range ds {
		if d.Notice != "" {
			notices++
		}
	}
	if notices != 2 {
		t.Errorf("notices = %d, want 2", notices)
	}
}

func TestOpenAI_429_RetryAfterSecondsHonored(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	// Retry-After: 1 must stretch the wait beyond the 500ms default.
	p, _ := openai429ThenOK(t, 1, "1")
	start := time.Now()
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	drainDeltas(t, ch)
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Errorf("elapsed = %v, want >= ~1s (Retry-After honoured)", elapsed)
	}
}

// --- integration: Anthropic provider parity ---

func TestAnthropic_429_RetryAndNotice(t *testing.T) {
	var calls int32
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error"}}`))
			return
		}
		sseResponse(w,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
			`{"type":"message_stop"}`,
		)
	})
	p, err := NewAnthropic(AnthropicConfig{BaseURL: srv.URL, APIKey: "key", Model: "claude-sonnet"})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ds := drainDeltas(t, ch)
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("calls = %d, want 2", got)
	}
	var text strings.Builder
	var notices []string
	for _, d := range ds {
		text.WriteString(d.Content)
		if d.Notice != "" {
			notices = append(notices, d.Notice)
		}
		if d.Err != nil {
			t.Fatalf("unexpected stream error: %v", d.Err)
		}
	}
	if text.String() != "ok" {
		t.Errorf("content = %q, want ok", text.String())
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "rate limited") {
		t.Errorf("notices = %v, want one rate-limit notice", notices)
	}
}

func TestAnthropic_429_ExhaustedSaysSwitchModel(t *testing.T) {
	var calls int32
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error"}}`))
	})
	p, err := NewAnthropic(AnthropicConfig{BaseURL: srv.URL, APIKey: "key", Model: "claude-sonnet"})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ds := drainDeltas(t, ch)
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("calls = %d, want 3 (maxAttempts)", got)
	}
	last := ds[len(ds)-1]
	if last.Err == nil {
		t.Fatal("expected terminal error")
	}
	msg := last.Err.Error()
	if !strings.Contains(msg, "http 429") || !strings.Contains(msg, "rate-limited") || !strings.Contains(msg, "/model") {
		t.Errorf("err = %q, want http 429 + actionable rate-limit hint", msg)
	}
}
