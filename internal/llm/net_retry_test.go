package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	if w := retryWait(h, 1, rateLimitWaitBudget); w < 250*time.Millisecond || w > 500*time.Millisecond {
		t.Fatalf("attempt 1: w=%v want jittered 250-500ms", w)
	}
	if w := retryWait(h, 2, rateLimitWaitBudget); w < 500*time.Millisecond || w > time.Second {
		t.Fatalf("attempt 2: w=%v want jittered 500ms-1s", w)
	}
}

func TestRetryableHTTPStatusRejectsPermanentClientErrors(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusUnprocessableEntity} {
		if isRetryableHTTPStatus(status) {
			t.Errorf("status %d must not be retried", status)
		}
	}
	for _, status := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout, 524, 529} {
		if !isRetryableHTTPStatus(status) {
			t.Errorf("status %d should be retried", status)
		}
	}
}

func TestProviderRetryAttemptsGives503LongerRecoveryWindow(t *testing.T) {
	if got := providerRetryAttempts(http.StatusServiceUnavailable); got != 5 {
		t.Fatalf("503 attempts = %d, want 5", got)
	}
	if got := providerRetryAttempts(http.StatusTooManyRequests); got != 3 {
		t.Fatalf("429 attempts = %d, want conservative 3", got)
	}
}

func TestHTTPResponseErrorCompactsGatewayRoutingMetadata(t *testing.T) {
	body := `{"error":{"message":"Service temporarily unavailable. Please try again shortly."},"providerMetadata":{"gateway":{"routing":"` + strings.Repeat("x", 700) + `"}}}`
	err := (&HTTPResponseError{Status: http.StatusServiceUnavailable, Body: body}).Error()
	if !strings.Contains(err, "Service temporarily unavailable") || strings.Contains(err, "providerMetadata") || len(err) > 200 {
		t.Fatalf("compacted error = %q", err)
	}
}

func TestRetryableProviderResponseHandlesWrappedModelUnavailable400(t *testing.T) {
	body := []byte(`{"error":{"type":"server_error","message":"Error from provider (Console): Upstream request failed: Model is unavailable."}}`)
	if !isRetryableProviderResponse(http.StatusBadRequest, body) {
		t.Fatal("wrapped model-unavailable 400 should be retried")
	}
	if !isRetryableProviderResponse(http.StatusUnprocessableEntity, body) {
		t.Fatal("wrapped model-unavailable 422 should be retried")
	}
	for _, body := range [][]byte{
		[]byte(`{"error":{"message":"invalid api key"}}`),
		[]byte(`{"error":{"message":"model does not exist"}}`),
		[]byte(`{"error":{"message":"unsupported reasoning_effort"}}`),
	} {
		if isRetryableProviderResponse(http.StatusBadRequest, body) {
			t.Fatalf("permanent 400 must not be retried: %s", body)
		}
	}
}

// --- integration: OpenAI provider over httptest ---

func TestOpenAI_503GetsFiveBoundedAttempts(t *testing.T) {
	var calls int32
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"message":"Service temporarily unavailable"}}`))
	})
	p, err := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "temporary-route"})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	deltas := drainDeltas(t, ch)
	if got := atomic.LoadInt32(&calls); got != 5 {
		t.Fatalf("calls = %d, want 5", got)
	}
	var notices int
	for _, delta := range deltas {
		if delta.Notice != "" {
			notices++
		}
	}
	if notices != 4 || deltas[len(deltas)-1].Err == nil {
		t.Fatalf("notices=%d terminal=%v", notices, deltas[len(deltas)-1].Err)
	}
}

func TestOpenAI_WrappedModelUnavailable400Retries(t *testing.T) {
	var calls int32
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"type":"server_error","message":"Error from provider (Console): Upstream request failed: Model is unavailable."}}`))
			return
		}
		sseResponse(w, `{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
	})
	p, err := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "minimax/minimax-m3:free"})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	deltas := drainDeltas(t, ch)
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("calls = %d, want 2", got)
	}
	var text strings.Builder
	for _, d := range deltas {
		if d.Err != nil {
			t.Fatalf("unexpected terminal error after retry: %v", d.Err)
		}
		text.WriteString(d.Content)
	}
	if text.String() != "ok" {
		t.Fatalf("text = %q, want ok", text.String())
	}
}

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

func TestOpenAI_EffortCorrectionDoesNotConsumeRateRetryBudget(t *testing.T) {
	t.Cleanup(func() { _ = SetReasoningEffort(""); clearReasoningEffortSupport() })
	if err := SetReasoningEffort("minimal"); err != nil {
		t.Fatal(err)
	}
	var calls int32
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch atomic.AddInt32(&calls, 1) {
		case 1:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Unsupported value: 'minimal' is not supported. Supported values are: 'none', 'low', 'medium', and 'high'.","param":"reasoning_effort"}}`))
		case 2, 3:
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"message":"temporarily unavailable"}}`))
		default:
			sseResponse(w, `{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
		}
	})
	p, err := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "gpt-5.5"})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, delta := range drainDeltas(t, ch) {
		if delta.Err != nil {
			t.Fatalf("unexpected error after independent retries: %v", delta.Err)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 4 {
		t.Fatalf("calls = %d, want effort correction + 2 transient failures + success", got)
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

func TestOpenAI_429LongRetryAfterFailsImmediatelyWithMetadata(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Retry-After", "3600")
		http.Error(w, `{"error":{"message":"daily limit"}}`, http.StatusTooManyRequests)
	}))
	defer srv.Close()
	p, err := NewOpenAI(OpenAIConfig{BaseURL: srv.URL + "/v1", Model: "limited"})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	stream, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got error
	for delta := range stream {
		if delta.Err != nil {
			got = delta.Err
		}
	}
	if got == nil {
		t.Fatal("expected 429 error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("long Retry-After slept for %v", elapsed)
	}
	if calls != 1 {
		t.Fatalf("server calls = %d, want 1", calls)
	}
	if retryAfter, ok := RateLimitRetryAfter(got); !ok || retryAfter != time.Hour {
		t.Fatalf("rate-limit metadata = %v, %v", retryAfter, ok)
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
