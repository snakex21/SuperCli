package llm

import (
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Logfare (https://logfare.ai/v1) is a free OpenAI-compatible aggregator
// that users add as a custom provider. Part of its catalog runs on
// Cloudflare Workers AI (model aliases such as @cf/baai/bge-m3, "served on
// Workers AI"), and provider errors pass through, so a Workers AI per-model
// limit reaches SuperCli as "AiError: AiError: rate limiting: inference
// request per min rate reached" even when the user's own Logfare account is
// far from its limits. testdata/logfare_models.json is a trimmed copy of the
// public GET /v1/models response (no key is needed for it).

const logfareRateLimitBody = `{"error":{"message":"Your account has reached its request budget. Limit reached: 40 requests per minute. Retry in 23 seconds.","type":"rate_limit_error","limits":[{"requests":40,"window_seconds":60}],"retry_after":23}}`

func TestLogfareModelListKeepsChatModelsAndReadsCapabilities(t *testing.T) {
	body, err := os.ReadFile("testdata/logfare_models.json")
	if err != nil {
		t.Fatal(err)
	}
	models, err := parseProviderModelInfos(body)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]ModelInfo{}
	for _, m := range models {
		byID[m.ID] = m
	}
	for _, nonChat := range []string{"flux-1-schnell", "bge-m3", "nova-3", "clef"} {
		if _, ok := byID[nonChat]; ok {
			t.Errorf("%s serves no chat endpoint and must not be offered as a chat model", nonChat)
		}
	}
	for id, wantTools := range map[string]bool{"logfare/auto": true, "gemma-4-26b": true, "glm-5.3-flash": true, "text-only-sample": false} {
		m, ok := byID[id]
		if !ok {
			t.Fatalf("chat model %s missing", id)
		}
		if m.ToolUse != wantTools {
			t.Errorf("%s ToolUse = %v, want %v", id, m.ToolUse, wantTools)
		}
	}
	if m := byID["gemma-4-26b"]; m.ContextLength != 256000 || !m.Vision || !m.VisionKnown {
		t.Errorf("gemma-4-26b metadata = %+v", m)
	}
	if m := byID["text-only-sample"]; m.Vision || !m.VisionKnown || m.ContextLength != 24576 {
		t.Errorf("text-only metadata = %+v", m)
	}
	if len(models) != 4 {
		t.Errorf("chat models = %d, want 4", len(models))
	}
}

func TestLogfareRateLimitHonoursRetryAfterHeaderOrBody(t *testing.T) {
	for _, header := range []bool{true, false} {
		withMaxRetries(t, 5)
		waits := fakeRetrySleep(t)
		var calls int32
		srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			if atomic.AddInt32(&calls, 1) == 1 {
				if header {
					w.Header().Set("Retry-After", "23")
				}
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = io.WriteString(w, logfareRateLimitBody)
				return
			}
			sseResponse(w, `{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
		})
		p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL + "/v1", Model: "logfare/auto"})
		if _, err := completeOnce(t, p, nil); err != nil {
			t.Fatal(err)
		}
		if calls != 2 || len(*waits) != 1 || (*waits)[0] != 23*time.Second {
			t.Fatalf("header=%v calls=%d waits=%v, want one 23 s wait", header, calls, *waits)
		}
	}
}

func TestLogfareDailyBudgetIsSurfacedNotRetried(t *testing.T) {
	withMaxRetries(t, UnlimitedRetries)
	fakeRetrySleep(t)
	var calls int32
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"Your account has reached its request budget. Limit reached: 5000 requests per day. Retry in 40000 seconds.","type":"rate_limit_error","limits":[{"requests":5000,"window_seconds":86400}],"retry_after":40000}}`)
	})
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL + "/v1", Model: "logfare/auto"})
	_, err := completeOnce(t, p, nil)
	if err == nil || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	if after, ok := RateLimitRetryAfter(err); !ok || after != 40000*time.Second {
		t.Fatalf("fallback metadata = %v %v", after, ok)
	}
}

func TestLogfarePassThroughWorkersAILimitIsPerMinuteTransient(t *testing.T) {
	withMaxRetries(t, 0)
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		sseResponse(w, `{"error":{"message":"`+cfUserStreamError+`"}}`)
	})
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL + "/v1", Model: "logfare/auto"})
	_, err := completeOnce(t, p, nil)
	if ClassifyProviderError(err) != RetryTransient || !IsPerMinuteRateLimit(err) {
		t.Fatalf("err=%v class=%v", err, ClassifyProviderError(err))
	}
	if !strings.Contains(err.Error(), "provider stream error: AiError") {
		t.Fatalf("historical text changed: %v", err)
	}
}
