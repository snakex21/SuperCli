package agent

// Audit-130: end-to-end transport faults through the REAL OpenAI Chat
// Completions / Responses providers and the agent loop (127.0.0.1 mock).
// docs/configuration.md ("Retries and request limits") promises that dropped
// connections, timeouts and short outages are retried, and that an in-stream
// error before any tool call / visible text is re-requested.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func a130AgentKnownFailing(t *testing.T, issue string) {
	t.Helper()
	if os.Getenv("SUPERCLI_AUDIT_KNOWN_FAILING") != "1" {
		t.Skip("KNOWN-FAILING audit-130: " + issue)
	}
}

type a130AgentMock struct {
	srv      *httptest.Server
	requests atomic.Int32
}

func newA130AgentMock(t *testing.T, handle func(w http.ResponseWriter, r *http.Request, n int)) *a130AgentMock {
	t.Helper()
	m := &a130AgentMock{}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !(strings.HasSuffix(r.URL.Path, "/chat/completions") || strings.HasSuffix(r.URL.Path, "/responses")) {
			http.NotFound(w, r)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		handle(w, r, int(m.requests.Add(1)))
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func a130AgentSSE(w http.ResponseWriter, lines ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	for _, l := range lines {
		fmt.Fprint(w, l)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
}

func a130AgentJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func a130AgentText(wire, text string) string {
	if wire == "chat" {
		return "data: " + a130AgentJSON(map[string]any{"id": "c", "object": "chat.completion.chunk",
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": text}}}}) + "\n\n"
	}
	return "data: " + a130AgentJSON(map[string]any{"type": "response.output_text.delta", "output_index": 0, "delta": text}) + "\n\n"
}

func a130AgentToolStart(wire, args string) []string {
	if wire == "chat" {
		return []string{"data: " + a130AgentJSON(map[string]any{"id": "c", "object": "chat.completion.chunk",
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "probe", "arguments": args}}}}}}}) + "\n\n"}
	}
	return []string{
		"data: " + a130AgentJSON(map[string]any{"type": "response.output_item.added", "output_index": 0,
			"item": map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": "probe"}}) + "\n\n",
		"data: " + a130AgentJSON(map[string]any{"type": "response.function_call_arguments.delta", "item_id": "fc_1", "output_index": 0, "delta": args}) + "\n\n",
	}
}

func a130AgentFinish(wire string) string {
	if wire == "chat" {
		return "data: " + a130AgentJSON(map[string]any{"id": "c", "object": "chat.completion.chunk",
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12}}) + "\n\ndata: [DONE]\n\n"
	}
	return "data: " + a130AgentJSON(map[string]any{"type": "response.completed", "response": map[string]any{
		"output": []any{}, "usage": map[string]any{"input_tokens": 10, "output_tokens": 2, "total_tokens": 12}}}) + "\n\n"
}

func a130AgentProvider(t *testing.T, wire, url string, timeout time.Duration) llm.Provider {
	t.Helper()
	var (
		p   llm.Provider
		err error
	)
	if wire == "chat" {
		p, err = llm.NewOpenAI(llm.OpenAIConfig{BaseURL: url + "/v1", APIKey: "dummy", Model: "mock-model", Timeout: timeout, Capabilities: llm.NewCapabilityRegistry()})
	} else {
		p, err = llm.NewResponses(llm.ResponsesConfig{BaseURL: url + "/v1", APIKey: "dummy", Model: "mock-model", Timeout: timeout, Capabilities: llm.NewCapabilityRegistry()})
	}
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// a130FastAgentRetry removes the 1 s+ agent backoff without changing the
// retry decision itself.
func a130FastAgentRetry(t *testing.T, maxRetries int) {
	t.Helper()
	saved, unknown := llm.MaxRetries(), llm.RetryUnknownErrors()
	savedWait, savedBackoff := streamRetryWait, streamRetryBackoff
	llm.SetMaxRetries(maxRetries)
	llm.SetRetryUnknownErrors(false)
	streamRetryBackoff = func(int, error) time.Duration { return time.Millisecond }
	streamRetryWait = func(ctx context.Context, d time.Duration) error { return ctx.Err() }
	t.Cleanup(func() {
		llm.SetMaxRetries(saved)
		llm.SetRetryUnknownErrors(unknown)
		streamRetryWait, streamRetryBackoff = savedWait, savedBackoff
	})
}

type a130Outcome struct {
	done    bool
	err     error
	text    string
	notices []string
	elapsed time.Duration
}

func a130RunAgent(t *testing.T, p llm.Provider, prompt string, limit time.Duration) a130Outcome {
	t.Helper()
	reg := tools.NewRegistry()
	var executed atomic.Int32
	reg.MustRegister(tools.Tool{Name: "probe", Description: "probe", Schema: `{"type":"object","properties":{"q":{"type":"string"}}}`,
		Fn: func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
			executed.Add(1)
			return tools.Result{Text: "probed"}, nil
		}})
	reg.MarkAlwaysOn("probe")
	l := makeLoop(t, p, reg, "system")
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	start := time.Now()
	ch, err := l.Run(ctx, prompt)
	if err != nil {
		t.Fatal(err)
	}
	var out a130Outcome
	for ev := range ch {
		switch e := ev.(type) {
		case DoneEvent:
			out.done = true
		case ErrorEvent:
			out.err = e.Err
		case MessageEvent:
			out.text += e.Text
		case NoticeEvent:
			out.notices = append(out.notices, e.Text)
		}
	}
	out.elapsed = time.Since(start)
	if executed.Load() != 0 {
		t.Fatalf("a partial/garbled tool call was executed %d times", executed.Load())
	}
	return out
}

// E1. The connection is dropped before any response bytes (upstream
// restart, LB reset), then the endpoint recovers. Documented: retried.
func TestA130Agent_DroppedConnectionIsRetried(t *testing.T) {
	for _, wire := range []string{"chat", "responses"} {
		t.Run(wire, func(t *testing.T) {
			a130FastAgentRetry(t, 5)
			m := newA130AgentMock(t, func(w http.ResponseWriter, r *http.Request, n int) {
				if n == 1 {
					if hj, ok := w.(http.Hijacker); ok {
						if c, _, err := hj.Hijack(); err == nil {
							c.Close()
						}
					}
					return
				}
				a130AgentSSE(w, a130AgentText(wire, "recovered"), a130AgentFinish(wire))
			})
			out := a130RunAgent(t, a130AgentProvider(t, wire, m.srv.URL, 5*time.Second), "hello", 20*time.Second)
			t.Logf("%s: done=%v err=%v requests=%d notices=%q elapsed=%s", wire, out.done, out.err, m.requests.Load(), out.notices, out.elapsed)
			if !out.done || m.requests.Load() != 2 {
				a130AgentKnownFailing(t, fmt.Sprintf("a connection dropped before the response is NOT retried by the agent with the real %s provider (requests=%d, err=%v): providers deliver transport errors in-stream, the agent only replays providerCompleteError startup failures, and the in-stream text classifies as unknown", wire, m.requests.Load(), out.err))
				t.Fatalf("not retried")
			}
		})
	}
}

// E2. The stream breaks (unexpected EOF) after the role/first frame but
// before any visible text or tool call (here: inside partial tool-call
// arguments). Documented: "an in-stream error is retried only when the
// failed attempt produced no tool call and no visible answer text".
func TestA130Agent_MidStreamDropBeforeVisibleOutputIsRetried(t *testing.T) {
	for _, wire := range []string{"chat", "responses"} {
		t.Run(wire, func(t *testing.T) {
			a130FastAgentRetry(t, 5)
			m := newA130AgentMock(t, func(w http.ResponseWriter, r *http.Request, n int) {
				if n == 1 {
					a130AgentSSE(w, a130AgentToolStart(wire, `{"q":"par`)...)
					panic(http.ErrAbortHandler)
				}
				a130AgentSSE(w, a130AgentText(wire, "recovered"), a130AgentFinish(wire))
			})
			out := a130RunAgent(t, a130AgentProvider(t, wire, m.srv.URL, 5*time.Second), "hello", 20*time.Second)
			t.Logf("%s: done=%v err=%v requests=%d elapsed=%s", wire, out.done, out.err, m.requests.Load(), out.elapsed)
			if !out.done || m.requests.Load() != 2 {
				a130AgentKnownFailing(t, fmt.Sprintf("mid-stream 'unexpected EOF' before any visible output is not re-requested with the real %s provider (requests=%d, err=%v): classified unknown, retried only with retry_unknown_errors", wire, m.requests.Load(), out.err))
				t.Fatalf("not retried")
			}
		})
	}
}

// E3. Silent stall after headers: the idle watchdog fires. The run must end
// with an error that says it timed out (not look like a user cancel), and
// per the docs a timeout is retried.
func TestA130Agent_IdleStallSurfacesAsTimeoutAndRetries(t *testing.T) {
	for _, wire := range []string{"chat", "responses"} {
		t.Run(wire, func(t *testing.T) {
			a130FastAgentRetry(t, 5)
			m := newA130AgentMock(t, func(w http.ResponseWriter, r *http.Request, n int) {
				if n == 1 {
					a130AgentSSE(w, ": open\n\n")
					<-r.Context().Done()
					return
				}
				a130AgentSSE(w, a130AgentText(wire, "recovered"), a130AgentFinish(wire))
			})
			out := a130RunAgent(t, a130AgentProvider(t, wire, m.srv.URL, 700*time.Millisecond), "hello", 20*time.Second)
			t.Logf("%s: done=%v err=%v canceledLike=%v requests=%d elapsed=%s", wire, out.done, out.err, errors.Is(out.err, context.Canceled), m.requests.Load(), out.elapsed)
			if out.elapsed > 5*time.Second {
				t.Fatalf("stall not bounded by the idle timeout: %s", out.elapsed)
			}
			if !out.done {
				a130AgentKnownFailing(t, fmt.Sprintf("idle-timeout stall ends the run with %q (errors.Is(context.Canceled)=%v) and no retry (%s)", out.err, errors.Is(out.err, context.Canceled), wire))
				t.Fatalf("stall not retried")
			}
		})
	}
}

// E4. Baseline: an HTTP 503 burst is retried by the provider and the run
// completes (the documented path that works).
func TestA130Agent_HTTP503BurstRecovers(t *testing.T) {
	for _, wire := range []string{"chat", "responses"} {
		t.Run(wire, func(t *testing.T) {
			a130FastAgentRetry(t, 5)
			m := newA130AgentMock(t, func(w http.ResponseWriter, r *http.Request, n int) {
				if n <= 2 {
					w.Header().Set("Retry-After", "0")
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				a130AgentSSE(w, a130AgentText(wire, "fine"), a130AgentFinish(wire))
			})
			out := a130RunAgent(t, a130AgentProvider(t, wire, m.srv.URL, 5*time.Second), "hello", 20*time.Second)
			if !out.done || m.requests.Load() != 3 || !strings.Contains(out.text, "fine") {
				t.Fatalf("%s: done=%v err=%v requests=%d text=%q", wire, out.done, out.err, m.requests.Load(), out.text)
			}
		})
	}
}
