package llm

// Audit-130 robustness helpers: a scriptable 127.0.0.1 mock for the
// OpenAI Chat Completions SSE and Responses SSE wire formats, a stream
// collector and a goroutine-leak settle check. Test-only; no product code.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func a130Soak(t *testing.T) {
	t.Helper()
	if os.Getenv("SUPERCLI_AUDIT_SOAK") != "1" {
		t.Skip("audit-130 soak test; set SUPERCLI_AUDIT_SOAK=1")
	}
}

// a130Mock counts model requests (POST .../chat/completions or .../responses)
// and hands each one, with its 1-based number, to handle.
type a130Mock struct {
	srv      *httptest.Server
	requests atomic.Int32
	mu       sync.Mutex
	arrivals []time.Time
	bodies   [][]byte
	clock    func() time.Time
	handle   func(w http.ResponseWriter, r *http.Request, n int)
}

func newA130Mock(t *testing.T, handle func(w http.ResponseWriter, r *http.Request, n int)) *a130Mock {
	t.Helper()
	m := &a130Mock{handle: handle, clock: time.Now}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !(strings.HasSuffix(r.URL.Path, "/chat/completions") || strings.HasSuffix(r.URL.Path, "/responses")) {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		n := int(m.requests.Add(1))
		m.mu.Lock()
		m.arrivals = append(m.arrivals, m.clock())
		m.bodies = append(m.bodies, body)
		m.mu.Unlock()
		m.handle(w, r, n)
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *a130Mock) URL() string { return m.srv.URL + "/v1" }

func (m *a130Mock) Arrivals() []time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]time.Time(nil), m.arrivals...)
}

func (m *a130Mock) Body(i int) map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	var v map[string]any
	if i < len(m.bodies) {
		_ = json.Unmarshal(m.bodies[i], &v)
	}
	return v
}

// --- SSE writers ---

func a130SSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func a130Write(w http.ResponseWriter, s string) bool {
	if _, err := fmt.Fprint(w, s); err != nil {
		return false
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return true
}

func a130JSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func chatContentLine(text string) string {
	return "data: " + a130JSON(map[string]any{"id": "c1", "object": "chat.completion.chunk",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": text}}}}) + "\n\n"
}

func chatToolLine(id, name, args string) string {
	fn := map[string]any{"arguments": args}
	if name != "" {
		fn["name"] = name
	}
	call := map[string]any{"index": 0, "type": "function", "function": fn}
	if id != "" {
		call["id"] = id
	}
	return "data: " + a130JSON(map[string]any{"id": "c1", "object": "chat.completion.chunk",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{call}}}}}) + "\n\n"
}

func chatFinishLine(reason string) string {
	return "data: " + a130JSON(map[string]any{"id": "c1", "object": "chat.completion.chunk",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": reason}},
		"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}}) + "\n\n"
}

const chatDoneLine = "data: [DONE]\n\n"

func respLine(ev map[string]any) string { return "data: " + a130JSON(ev) + "\n\n" }

func respTextLine(text string) string {
	return respLine(map[string]any{"type": "response.output_text.delta", "output_index": 0, "delta": text})
}

func respToolAddedLine(name string) string {
	return respLine(map[string]any{"type": "response.output_item.added", "output_index": 0,
		"item": map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": name, "arguments": ""}})
}

func respArgsDeltaLine(delta string) string {
	return respLine(map[string]any{"type": "response.function_call_arguments.delta", "item_id": "fc_1", "output_index": 0, "delta": delta})
}

func respToolDoneLines(name, args string) string {
	item := map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": name, "arguments": args}
	return respLine(map[string]any{"type": "response.function_call_arguments.done", "item_id": "fc_1", "output_index": 0, "arguments": args}) +
		respLine(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
}

func respCompletedLine(output []any) string {
	if output == nil {
		output = []any{}
	}
	return respLine(map[string]any{"type": "response.completed", "response": map[string]any{
		"id": "r1", "status": "completed", "output": output,
		"usage": map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}}})
}

// --- providers ---

type a130Wire string

const (
	wireChat      a130Wire = "chat"
	wireResponses a130Wire = "responses"
)

var a130Wires = []a130Wire{wireChat, wireResponses}

// a130Provider builds the real production provider for the wire against the
// mock. The HTTP client is a clone of the stock transport (what NewOpenAI
// builds itself) so idle connections can be closed before leak checks.
func a130Provider(t *testing.T, wire a130Wire, baseURL string, timeout time.Duration) (Provider, *http.Transport) {
	t.Helper()
	tr := http.DefaultTransport.(*http.Transport).Clone()
	client := &http.Client{Transport: tr}
	t.Cleanup(tr.CloseIdleConnections)
	switch wire {
	case wireChat:
		p, err := NewOpenAI(OpenAIConfig{BaseURL: baseURL, APIKey: "dummy", Model: "mock-model", Timeout: timeout, HTTPClient: client, Capabilities: NewCapabilityRegistry()})
		if err != nil {
			t.Fatal(err)
		}
		return p, tr
	default:
		p, err := NewResponses(ResponsesConfig{BaseURL: baseURL, APIKey: "dummy", Model: "mock-model", Timeout: timeout, HTTPClient: client, Capabilities: NewCapabilityRegistry()})
		if err != nil {
			t.Fatal(err)
		}
		return p, tr
	}
}

type a130Result struct {
	Content  string
	Calls    []ToolCall
	Err      error
	Notices  []string
	Finish   string
	Elapsed  time.Duration
	Deltas   int
	Closed   bool // channel closed (stream terminated)
	ArgBytes int
}

var a130Tools = []ToolDef{{Name: "write_file", Description: "write a file", Schema: `{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}}}`}}

// a130Run drains one Complete call, bounded by limit (the test fails if the
// stream does not terminate in time).
func a130Run(t *testing.T, ctx context.Context, p Provider, limit time.Duration) a130Result {
	t.Helper()
	start := time.Now()
	ch, err := p.Complete(ctx, []Message{{Role: RoleUser, Content: "hello"}}, a130Tools)
	if err != nil {
		return a130Result{Err: err, Elapsed: time.Since(start), Closed: true}
	}
	var res a130Result
	deadline := time.NewTimer(limit)
	defer deadline.Stop()
	for {
		select {
		case d, ok := <-ch:
			if !ok {
				res.Closed = true
				res.Elapsed = time.Since(start)
				return res
			}
			res.Deltas++
			res.Content += d.Content
			if d.ToolCall != nil {
				res.Calls = append(res.Calls, *d.ToolCall)
				res.ArgBytes += len(d.ToolCall.Arguments)
			}
			if d.Err != nil && res.Err == nil {
				res.Err = d.Err
			}
			if d.Notice != "" {
				res.Notices = append(res.Notices, d.Notice)
			}
			if d.FinishReason != "" {
				res.Finish = d.FinishReason
			}
		case <-deadline.C:
			res.Elapsed = time.Since(start)
			t.Fatalf("stream did not terminate within %s (deltas=%d content=%dB err=%v)", limit, res.Deltas, len(res.Content), res.Err)
			return res
		}
	}
}

// a130GoroutineBaseline returns a check that waits (settle loop, up to 5 s)
// for the goroutine count to fall back to the baseline + slack.
func a130GoroutineBaseline(t *testing.T) func(slack int, closers ...func()) int {
	t.Helper()
	runtime.GC()
	base := runtime.NumGoroutine()
	return func(slack int, closers ...func()) int {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			for _, c := range closers {
				c()
			}
			n := runtime.NumGoroutine()
			if n <= base+slack {
				return n - base
			}
			if time.Now().After(deadline) {
				buf := make([]byte, 1<<16)
				k := runtime.Stack(buf, true)
				t.Fatalf("goroutine leak: baseline=%d now=%d (slack %d)\n%s", base, n, slack, buf[:k])
				return n - base
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

func a130WithRetries(t *testing.T, n int, unknown bool) {
	t.Helper()
	saved, savedUnknown := MaxRetries(), RetryUnknownErrors()
	SetMaxRetries(n)
	SetRetryUnknownErrors(unknown)
	t.Cleanup(func() { SetMaxRetries(saved); SetRetryUnknownErrors(savedUnknown) })
}

// a130Hijack drops the TCP connection without writing anything else.
func a130Hijack(w http.ResponseWriter) {
	if hj, ok := w.(http.Hijacker); ok {
		if conn, _, err := hj.Hijack(); err == nil {
			_ = conn.Close()
		}
	}
}

// a130Abort aborts an already started (chunked) response mid-body: the
// client sees an unexpected EOF, exactly like a crashed upstream.
func a130Abort() { panic(http.ErrAbortHandler) }
