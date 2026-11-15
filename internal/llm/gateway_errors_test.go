package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Mock replays of Cloudflare Workers AI responses on the OpenAI-compatible
// endpoint (…/accounts/<id>/ai/v1/chat/completions). Formats follow
// developers.cloudflare.com/workers-ai/platform/errors and the in-stream
// error a user reported verbatim.

const (
	cfUserStreamError = `AiError: AiError: rate limiting: inference request per min rate reached (8ee05572-6d1f-4c5e-9d3e-2f0a2c1b7e11)`
	cfCapacityBody    = `{"result":null,"success":false,"errors":[{"code":3040,"message":"Capacity temporarily exceeded, please try again."}],"messages":[]}`
	cfDailyQuotaBody  = `{"result":null,"success":false,"errors":[{"code":3036,"message":"You have used up your daily free allocation of 10,000 neurons, please upgrade to Cloudflare's Workers Paid plan if you would like to continue usage."}],"messages":[]}`
	cfNoModelBody     = `{"result":null,"success":false,"errors":[{"code":5007,"message":"No such model @cf/meta/llama-9 or task"}],"messages":[]}`
	cfToolsBody       = `{"result":null,"success":false,"errors":[{"code":5006,"message":"AiError: Bad input: Error: Type mismatch of '/tools', 'array' not in 'string'"}],"messages":[]}`
	cfChatOK          = `{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"@cf/meta/llama-3.1-8b-instruct","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`
)

func cfModel() string { return "@cf/meta/llama-3.1-8b-instruct" }

func completeOnce(t *testing.T, p Provider, tools []ToolDef) ([]Delta, error) {
	t.Helper()
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, tools)
	if err != nil {
		return nil, err
	}
	deltas := drainDeltas(t, ch)
	for _, d := range deltas {
		if d.Err != nil {
			return deltas, d.Err
		}
	}
	return deltas, nil
}

func TestCloudflareCapacityEnvelopeIsRetried(t *testing.T) {
	withMaxRetries(t, 5)
	fakeRetrySleep(t)
	var calls int32
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, cfCapacityBody)
			return
		}
		sseResponse(w, cfChatOK)
	})
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: cfModel()})
	if _, err := completeOnce(t, p, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestCloudflareDailyQuotaAndBadModelAreNeverRetried(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		want   string
	}{
		"daily-quota": {http.StatusTooManyRequests, cfDailyQuotaBody, "daily free allocation"},
		"no-model":    {http.StatusBadRequest, cfNoModelBody, "No such model"},
	} {
		t.Run(name, func(t *testing.T) {
			withMaxRetries(t, UnlimitedRetries)
			SetRetryUnknownErrors(true)
			fakeRetrySleep(t)
			var calls int32
			srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: cfModel()})
			_, err := completeOnce(t, p, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) || calls != 1 {
				t.Fatalf("err=%v calls=%d", err, calls)
			}
			if ClassifyProviderError(err) != RetryPermanent {
				t.Fatalf("class = %v", ClassifyProviderError(err))
			}
		})
	}
}

func TestCloudflareEnvelopeErrorText(t *testing.T) {
	if got := errorTextFromBody([]byte(cfCapacityBody)); !strings.Contains(got, "Capacity temporarily exceeded") {
		t.Fatalf("envelope text = %q", got)
	}
}

func TestCloudflareInStreamErrorsAreTypedAndClassified(t *testing.T) {
	for name, tc := range map[string]struct {
		line string
		want RetryClass
		text string
	}{
		"user-rate-limit":   {`{"error":{"message":"` + cfUserStreamError + `"}}`, RetryTransient, cfUserStreamError},
		"user-rate-string":  {`{"error":"` + cfUserStreamError + `"}`, RetryTransient, cfUserStreamError},
		"envelope-capacity": {cfCapacityBody, RetryTransient, "Capacity temporarily exceeded"},
		"envelope-quota":    {cfDailyQuotaBody, RetryPermanent, "daily free allocation"},
		"gateway-limit":     {`{"error":"AiGatewayError: 2018: Rate limit exceeded"}`, RetryTransient, "2018"},
	} {
		t.Run(name, func(t *testing.T) {
			withMaxRetries(t, 0)
			srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				sseResponse(w, tc.line)
			})
			p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: cfModel()})
			_, err := completeOnce(t, p, nil)
			var streamErr *ProviderStreamError
			if !errors.As(err, &streamErr) || !strings.Contains(err.Error(), tc.text) {
				t.Fatalf("err = %v", err)
			}
			if got := ClassifyProviderError(fmt.Errorf("sse: %w", err)); got != tc.want {
				t.Fatalf("class = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPerMinuteLimitBackoffCrossesTheMinute(t *testing.T) {
	err := fmt.Errorf("sse: %w", &ProviderStreamError{Prefix: "provider stream error", Message: cfUserStreamError})
	if !IsPerMinuteRateLimit(err) {
		t.Fatal("the Workers AI message must be recognized as a per-minute limit")
	}
	saved := retryJitter
	retryJitter = func(floor, _ time.Duration) time.Duration { return floor } // worst case: shortest waits
	t.Cleanup(func() { retryJitter = saved })
	var total time.Duration
	for n := 1; n <= 3; n++ {
		total += RetryBackoffFor(err, n, time.Second)
	}
	if total <= time.Minute {
		t.Fatalf("three shortest per-minute waits total %v; must exceed 60 s", total)
	}
	if w := RetryBackoffFor(errors.New("connection reset by peer"), 1, time.Second); w > time.Second {
		t.Fatalf("ordinary errors keep the short backoff, got %v", w)
	}
	// HTTP 429 naming a per-minute limit without Retry-After.
	body := []byte(`{"error":{"message":"Rate limit reached for requests per min (RPM): Limit 20"}}`)
	if w, ok := providerRetryWait(http.StatusTooManyRequests, http.Header{}, body, 1); !ok || w < 10*time.Second {
		t.Fatalf("per-minute 429 wait = %v ok=%v", w, ok)
	}
}

func TestToolsUnsupportedDegradesOnceAndIsRemembered(t *testing.T) {
	t.Cleanup(resetToolsUnsupported)
	var calls int32
	var bodies []string
	var captured *string
	srv, captured := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		b := []byte(*captured)
		bodies = append(bodies, string(b))
		atomic.AddInt32(&calls, 1)
		if strings.Contains(string(b), `"tools"`) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, cfToolsBody)
			return
		}
		sseResponse(w, cfChatOK)
	})
	tools := []ToolDef{{Name: "read", Description: "read a file", Schema: `{"type":"object"}`}}
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: cfModel()})
	deltas, err := completeOnce(t, p, tools)
	if err != nil {
		t.Fatal(err)
	}
	var notice string
	for _, d := range deltas {
		if d.Notice != "" {
			notice = d.Notice
		}
	}
	if calls != 2 || err != nil || !strings.Contains(notice, "does not support tool calling") || !strings.Contains(notice, cfModel()) {
		t.Fatalf("calls=%d notice=%q err=%v", calls, notice, err)
	}
	// A fresh provider instance for the same endpoint+model skips the doomed attempt.
	p2, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: cfModel()})
	if _, err := completeOnce(t, p2, tools); err != nil {
		t.Fatal(err)
	}
	if calls != 3 || strings.Contains(bodies[2], `"tools"`) {
		t.Fatalf("remembered rejection not applied: calls=%d", calls)
	}
	// A malformed tool CALL in history is a different 400 and is not matched.
	if isToolsUnsupportedError(http.StatusBadRequest, []byte(`{"error":{"message":"invalid tool_call_id in messages[3]"}}`)) {
		t.Fatal("tool-call history errors must not drop tools")
	}
}
