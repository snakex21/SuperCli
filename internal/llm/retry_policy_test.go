package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func withMaxRetries(t *testing.T, n int) {
	t.Helper()
	saved, unknown := MaxRetries(), RetryUnknownErrors()
	SetMaxRetries(n)
	t.Cleanup(func() { SetMaxRetries(saved); SetRetryUnknownErrors(unknown) })
}

// fakeRetrySleep records requested waits without sleeping; cancel-aware.
func fakeRetrySleep(t *testing.T) *[]time.Duration {
	t.Helper()
	saved := retrySleep
	var mu sync.Mutex
	var waits []time.Duration
	retrySleep = func(ctx context.Context, d time.Duration) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		mu.Lock()
		waits = append(waits, d)
		mu.Unlock()
		return nil
	}
	t.Cleanup(func() { retrySleep = saved })
	return &waits
}

func TestSetMaxRetriesNormalizesInvalidValues(t *testing.T) {
	withMaxRetries(t, DefaultMaxRetries)
	for in, want := range map[int]int{0: 0, 7: 7, -1: UnlimitedRetries, -5: DefaultMaxRetries} {
		SetMaxRetries(in)
		if got := MaxRetries(); got != want {
			t.Errorf("SetMaxRetries(%d) -> %d, want %d", in, got, want)
		}
	}
	SetMaxRetries(3)
	if !RetryAllowed(2) || RetryAllowed(3) {
		t.Fatal("finite budget: 3 retries allowed, the 4th not")
	}
	if got := RetryProgress(2); got != "2/3" {
		t.Fatalf("progress = %q", got)
	}
	SetMaxRetries(UnlimitedRetries)
	if !RetryAllowed(1_000_000) {
		t.Fatal("unlimited mode must always allow another retry")
	}
	if got := RetryProgress(12); got != "12/∞" {
		t.Fatalf("progress = %q", got)
	}
	SetMaxRetries(0)
	if RetryAllowed(0) {
		t.Fatal("max_retries=0 must not retry")
	}
}

func TestRetryBackoffIsExponentialJitteredAndCapped(t *testing.T) {
	for n := 1; n <= 40; n++ {
		w := RetryBackoff(n, time.Second)
		nominal := time.Second << (n - 1)
		if n > 7 || nominal > MaxRetryBackoff {
			nominal = MaxRetryBackoff
		}
		if w < nominal/2 || w > nominal {
			t.Fatalf("retry %d: wait %v outside [%v, %v]", n, w, nominal/2, nominal)
		}
	}
}

func TestOpenAIRetriesStopAfterConfiguredCount(t *testing.T) {
	for _, n := range []int{0, 1, 3, 8} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			withMaxRetries(t, n)
			waits := fakeRetrySleep(t)
			var calls int32
			srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":{"message":"Service temporarily unavailable"}}`))
			})
			p, err := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "m"})
			if err != nil {
				t.Fatal(err)
			}
			ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			deltas := drainDeltas(t, ch)
			if got := atomic.LoadInt32(&calls); got != int32(n+1) {
				t.Fatalf("calls = %d, want %d", got, n+1)
			}
			if len(*waits) != n {
				t.Fatalf("waits = %v, want %d", *waits, n)
			}
			var last string
			for _, d := range deltas {
				if d.Notice != "" {
					last = d.Notice
				}
			}
			if n > 0 && !strings.Contains(last, fmt.Sprintf("(retry %d/%d)", n, n)) {
				t.Fatalf("last notice = %q", last)
			}
			if deltas[len(deltas)-1].Err == nil {
				t.Fatal("expected terminal error")
			}
		})
	}
}

func TestAnthropicAndCodexHonourConfiguredCount(t *testing.T) {
	withMaxRetries(t, 4)
	fakeRetrySleep(t)
	var calls int32
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(529)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`))
	})
	p, err := NewAnthropic(AnthropicConfig{BaseURL: srv.URL, APIKey: "k", Model: "claude-x"})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err == nil {
		drainDeltas(t, ch)
	}
	if got := atomic.LoadInt32(&calls); got != 5 {
		t.Fatalf("anthropic calls = %d, want 5", got)
	}

	atomic.StoreInt32(&calls, 0)
	codexSrv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadGateway)
	})
	cp, err := NewCodex(CodexConfig{BackendURL: codexSrv.URL, Model: "gpt-5-codex", Tokens: &fakeTokens{access: "t"}})
	if err != nil {
		t.Fatal(err)
	}
	cch, err := cp.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err == nil {
		drainDeltas(t, cch)
	}
	if got := atomic.LoadInt32(&calls); got != 5 {
		t.Fatalf("codex calls = %d, want 5", got)
	}
}

func TestUnlimitedRetriesStopPromptlyOnCancel(t *testing.T) {
	withMaxRetries(t, UnlimitedRetries)
	// Real (cancel-aware) sleeps: the provider asks for 30 s between tries.
	var calls int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 3 {
			// Cancel while the third wait is about to begin.
			time.AfterFunc(20*time.Millisecond, cancel)
		}
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	saved := retrySleep
	var sleeps int32
	retrySleep = func(c context.Context, d time.Duration) error {
		// Fast-forward the first two waits; the third waits for real.
		if atomic.AddInt32(&sleeps, 1) < 3 {
			return c.Err()
		}
		return saved(c, d)
	}
	t.Cleanup(func() { retrySleep = saved })
	p, err := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	ch, err := p.Complete(ctx, []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var notices []string
	for d := range ch {
		if d.Notice != "" {
			notices = append(notices, d.Notice)
		}
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("cancel took %v", elapsed)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("calls = %d, want 3", got)
	}
	if len(notices) == 0 || !strings.Contains(notices[len(notices)-1], "(retry 3/∞)") || !strings.Contains(notices[len(notices)-1], "retrying in 30s") {
		t.Fatalf("notices = %v", notices)
	}
}

func TestUnlimitedRetriesEventuallySucceed(t *testing.T) {
	withMaxRetries(t, UnlimitedRetries)
	waits := fakeRetrySleep(t)
	var calls int32
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) <= 25 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		sseResponse(w, `{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
	})
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "m"})
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range drainDeltas(t, ch) {
		if d.Err != nil {
			t.Fatal(d.Err)
		}
	}
	if len(*waits) != 25 {
		t.Fatalf("waits = %d", len(*waits))
	}
	for _, w := range *waits {
		if w > MaxRetryBackoff {
			t.Fatalf("wait %v above cap", w)
		}
	}
}

func TestRetryAfterIsHonoured(t *testing.T) {
	withMaxRetries(t, 3)
	waits := fakeRetrySleep(t)
	var calls int32
	date := time.Now().Add(20 * time.Second).UTC().Format(http.TimeFormat)
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch atomic.AddInt32(&calls, 1) {
		case 1:
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusServiceUnavailable)
		case 2:
			w.Header().Set("Retry-After", date)
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			sseResponse(w, `{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
		}
	})
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "m"})
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	drainDeltas(t, ch)
	if len(*waits) != 2 || (*waits)[0] != 7*time.Second || (*waits)[1] < 18*time.Second || (*waits)[1] > 20*time.Second {
		t.Fatalf("waits = %v, want [7s ~20s]", *waits)
	}
}

func TestNonRetryableStatusesAreNeverRetried(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		for _, tc := range []struct {
			status int
			body   string
		}{
			{400, `{"error":{"message":"invalid request: bad field"}}`},
			{401, `{"error":{"message":"invalid api key"}}`},
			{403, `{"error":{"message":"forbidden"}}`},
			{404, `{"error":{"message":"model does not exist"}}`},
			{400, `{"error":{"code":"context_length_exceeded","message":"This model's maximum context length is 8192 tokens"}}`},
		} {
			t.Run(fmt.Sprintf("%v/%d", unknown, tc.status), func(t *testing.T) {
				withMaxRetries(t, UnlimitedRetries)
				SetRetryUnknownErrors(unknown)
				fakeRetrySleep(t)
				var calls int32
				srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
					atomic.AddInt32(&calls, 1)
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
				})
				p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "m"})
				ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
				if err == nil {
					drainDeltas(t, ch)
				}
				if got := atomic.LoadInt32(&calls); got != 1 {
					t.Fatalf("calls = %d, want 1", got)
				}
			})
		}
	}
}

func TestRetryUnknownErrorsRetriesUnclassifiedStatus(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		withMaxRetries(t, 2)
		SetRetryUnknownErrors(unknown)
		fakeRetrySleep(t)
		var calls int32
		srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.WriteHeader(http.StatusConflict) // 409: neither transient nor permanent
		})
		p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "m"})
		ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
		if err == nil {
			drainDeltas(t, ch)
		}
		want := int32(1)
		if unknown {
			want = 3
		}
		if got := atomic.LoadInt32(&calls); got != want {
			t.Fatalf("unknown=%v calls = %d, want %d", unknown, got, want)
		}
	}
}

func TestClassifyProviderErrors(t *testing.T) {
	cloudflare := "AiError: AiError: rate limiting: inference request per min rate reached (8ee05572-0000-4000-8000-000000000000)"
	cases := []struct {
		name string
		err  error
		want RetryClass
	}{
		{"cloudflare-stream", fmt.Errorf("sse: %w", openAIStreamError([]byte(`{"message":"`+cloudflare+`"}`))), RetryTransient},
		{"cloudflare-string", fmt.Errorf("sse: %w", openAIStreamError([]byte(`"`+cloudflare+`"`))), RetryTransient},
		{"cloudflare-text", errors.New("sse: provider stream error: " + cloudflare), RetryTransient},
		{"openai-stream-429", openAIStreamError([]byte(`{"message":"Rate limit reached for gpt-x","type":"requests","code":"rate_limit_exceeded"}`)), RetryTransient},
		{"openai-stream-overload", openAIStreamError([]byte(`{"message":"The server had an error while processing your request","type":"server_error","code":503}`)), RetryTransient},
		{"openrouter-stream", openAIStreamError([]byte(`{"code":429,"message":"Provider returned error","metadata":{"raw":"upstream"}}`)), RetryTransient},
		{"openrouter-overloaded", openAIStreamError([]byte(`{"code":502,"message":"Overloaded"}`)), RetryTransient},
		{"anthropic-overloaded", anthropicStreamError("overloaded_error", "Overloaded"), RetryTransient},
		{"anthropic-rate", anthropicStreamError("rate_limit_error", "Number of request tokens has exceeded your per-minute rate limit"), RetryTransient},
		{"responses-failed", responsesStreamError("responses", "server_is_overloaded", "The server is overloaded, please try again later"), RetryTransient},
		{"http-529", &HTTPResponseError{Status: 529, Body: `{"type":"error","error":{"type":"overloaded_error"}}`}, RetryTransient},
		{"http-429-openrouter", &HTTPResponseError{Status: 429, Body: `{"error":{"code":429,"message":"Rate limit exceeded: free-models-per-min"}}`}, RetryTransient},
		{"context", openAIStreamError([]byte(`{"code":"context_length_exceeded","message":"maximum context length is 8192 tokens"}`)), RetryPermanent},
		{"anthropic-invalid", anthropicStreamError("invalid_request_error", "prompt is too long"), RetryPermanent},
		{"auth", &HTTPResponseError{Status: 401, Body: "nope"}, RetryPermanent},
		{"forbidden", &HTTPResponseError{Status: 403, Body: "rate limit for org"}, RetryPermanent},
		{"notfound", &HTTPResponseError{Status: 404, Body: "{}"}, RetryPermanent},
		{"quota", openAIStreamError([]byte(`{"code":"insufficient_quota","message":"You exceeded your current quota"}`)), RetryPermanent},
		{"canceled", context.Canceled, RetryPermanent},
		{"unknown", openAIStreamError([]byte(`{"message":"something odd happened"}`)), RetryUnknown},
	}
	for _, tc := range cases {
		if got := ClassifyProviderError(tc.err); got != tc.want {
			t.Errorf("%s: class = %v, want %v (%v)", tc.name, got, tc.want, tc.err)
		}
	}
	if got := openAIStreamError([]byte(`{"message":"` + cloudflare + `"}`)).Error(); got != "provider stream error: "+cloudflare {
		t.Fatalf("historical text changed: %q", got)
	}
}
