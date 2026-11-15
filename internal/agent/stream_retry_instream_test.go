package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// The exact in-stream error a Cloudflare Workers AI user reported; it arrives
// as an SSE error chunk, not an HTTP 429.
const cloudflarePerMinute = "AiError: AiError: rate limiting: inference request per min rate reached (8ee05572-0000-4000-8000-000000000000)"

func inStreamErr(msg string) error {
	return fmt.Errorf("sse: %w", &llm.ProviderStreamError{Prefix: "provider stream error", Message: msg})
}

func TestInStreamRateLimitIsRetried(t *testing.T) {
	for name, frames := range map[string][]llm.Delta{
		"first-event":          {{Err: inStreamErr(cloudflarePerMinute)}},
		"after-reasoning":      {{Reasoning: "thinking"}, {Err: inStreamErr(cloudflarePerMinute)}},
		"anthropic-overloaded": {{Err: fmt.Errorf("sse: %w", &llm.ProviderStreamError{Prefix: "anthropic", Type: "overloaded_error", Message: "Overloaded"})}},
		"openrouter-429":       {{Err: fmt.Errorf("sse: %w", &llm.ProviderStreamError{Prefix: "provider stream error", Code: "429", Message: "Provider returned error"})}},
	} {
		t.Run(name, func(t *testing.T) {
			_, waits := fakeOutageClock(t)
			p := &startupRetryProvider{run: func(call int) (<-chan llm.Delta, error) {
				if call < 3 {
					return startupRetryStream(frames...), nil
				}
				return startupRetryStream(llm.Delta{Content: "ok", FinishReason: "stop"}), nil
			}}
			l := makeLoop(t, p, tools.NewRegistry(), "")
			out := make(chan Event, 32)
			text, _, _, err := l.completeWithStreamRetry(context.Background(), l.prepareToolRequest(), out)
			if err != nil || text != "ok" || p.calls.Load() != 3 || len(*waits) != 2 {
				t.Fatalf("text=%q err=%v calls=%d waits=%v", text, err, p.calls.Load(), *waits)
			}
			// History gets only the successful attempt — no duplicated partial output.
			if strings.Contains(text, "thinking") {
				t.Fatalf("failed attempt leaked into the reply: %q", text)
			}
		})
	}
}

func TestInStreamRetryStopsAfterMaxRetries(t *testing.T) {
	fakeOutageClock(t)
	llm.SetMaxRetries(2)
	cause := inStreamErr(cloudflarePerMinute)
	p := &startupRetryProvider{run: func(int) (<-chan llm.Delta, error) { return startupRetryStream(llm.Delta{Err: cause}), nil }}
	l := makeLoop(t, p, tools.NewRegistry(), "")
	_, _, _, err := l.completeWithStreamRetry(context.Background(), l.prepareToolRequest(), make(chan Event, 32))
	if !errors.Is(err, cause) || p.calls.Load() != 3 {
		t.Fatalf("err=%v calls=%d", err, p.calls.Load())
	}
}

func TestInStreamErrorNotRetriedAfterToolCallOrVisibleText(t *testing.T) {
	for name, frame := range map[string]llm.Delta{
		"tool":    {ToolCall: &llm.ToolCall{ID: "one", Name: "fixture_mutation", Arguments: `{}`}},
		"content": {Content: "partial answer"},
	} {
		t.Run(name, func(t *testing.T) {
			fakeOutageClock(t)
			cause := inStreamErr(cloudflarePerMinute)
			p := &startupRetryProvider{run: func(int) (<-chan llm.Delta, error) {
				return startupRetryStream(frame, llm.Delta{Err: cause}), nil
			}}
			l := makeLoop(t, p, tools.NewRegistry(), "")
			_, _, _, err := l.completeWithStreamRetry(context.Background(), l.prepareToolRequest(), make(chan Event, 32))
			if !errors.Is(err, cause) || p.calls.Load() != 1 {
				t.Fatalf("err=%v calls=%d", err, p.calls.Load())
			}
		})
	}
}

func TestInStreamPermanentAndUnknownErrors(t *testing.T) {
	permanent := map[string]error{
		"context-overflow": inStreamErr("This model's maximum context length is 8192 tokens"),
		"context-code":     fmt.Errorf("sse: %w", &llm.ProviderStreamError{Prefix: "provider stream error", Code: "context_length_exceeded", Message: "too long"}),
		"auth":             fmt.Errorf("sse: %w", &llm.ProviderStreamError{Prefix: "anthropic", Type: "authentication_error", Message: "invalid x-api-key"}),
		"invalid":          fmt.Errorf("sse: %w", &llm.ProviderStreamError{Prefix: "anthropic", Type: "invalid_request_error", Message: "bad"}),
		"not-found":        fmt.Errorf("sse: %w", &llm.ProviderStreamError{Prefix: "provider stream error", Code: "404", Message: "model not found"}),
	}
	unknown := inStreamErr("something unexpected happened")
	for _, retryUnknown := range []bool{false, true} {
		for name, cause := range permanent {
			t.Run(fmt.Sprintf("%s/unknown=%v", name, retryUnknown), func(t *testing.T) {
				fakeOutageClock(t)
				llm.SetMaxRetries(llm.UnlimitedRetries)
				llm.SetRetryUnknownErrors(retryUnknown)
				p := &startupRetryProvider{run: func(int) (<-chan llm.Delta, error) { return startupRetryStream(llm.Delta{Err: cause}), nil }}
				l := makeLoop(t, p, tools.NewRegistry(), "")
				_, _, _, err := l.completeWithStreamRetry(context.Background(), l.prepareToolRequest(), make(chan Event, 32))
				if !errors.Is(err, cause) || p.calls.Load() != 1 {
					t.Fatalf("err=%v calls=%d", err, p.calls.Load())
				}
			})
		}
		t.Run(fmt.Sprintf("unknown/unknown=%v", retryUnknown), func(t *testing.T) {
			fakeOutageClock(t)
			llm.SetRetryUnknownErrors(retryUnknown)
			p := &startupRetryProvider{run: func(int) (<-chan llm.Delta, error) { return startupRetryStream(llm.Delta{Err: unknown}), nil }}
			l := makeLoop(t, p, tools.NewRegistry(), "")
			_, _, _, err := l.completeWithStreamRetry(context.Background(), l.prepareToolRequest(), make(chan Event, 32))
			want := int32(1)
			if retryUnknown {
				want = 1 + llm.DefaultMaxRetries
			}
			if !errors.Is(err, unknown) || p.calls.Load() != want {
				t.Fatalf("err=%v calls=%d want=%d", err, p.calls.Load(), want)
			}
		})
	}
}

// A per-minute in-stream limit without Retry-After must not burn the retries
// inside the same minute: the default agent backoff starts at
// llm.PerMinuteRetryBase for it and stays short for transport drops.
func TestInStreamPerMinuteLimitUsesMinuteBackoff(t *testing.T) {
	perMinute := inStreamErr(cloudflarePerMinute)
	var total time.Duration
	for n := 1; n <= 3; n++ {
		total += streamRetryBackoff(n, perMinute)
	}
	if total <= time.Minute {
		t.Fatalf("three per-minute waits total %v; must cross into the next minute", total)
	}
	if w := streamRetryBackoff(1, errHTTP2Reset); w > time.Second {
		t.Fatalf("transport drop wait = %v", w)
	}
}
