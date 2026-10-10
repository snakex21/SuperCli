package webgui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

// Ordinary tests skip this opt-in fixture. Synthetic mode uses real Responses
// preparation/parsing with a fake transport; LIVE keeps the factory's real client.
type chatPhaseProbeTraceKey struct{}

type chatPhaseProbePoint struct {
	Name string `json:"name"`
	NS   int64  `json:"ns"`
}

type chatPhaseProbeCall struct {
	Purpose   string `json:"purpose"`
	TTFTNS    int64  `json:"ttft_ns"`
	Duration  int64  `json:"duration_ns"`
	Input     int    `json:"input"`
	Output    int    `json:"output"`
	Cached    int    `json:"cached_input"`
	Reasoning int    `json:"reasoning"`
	Canceled  bool   `json:"canceled"`
	Failed    bool   `json:"failed"`
}

type chatPhaseProbeTrace struct {
	mu                sync.Mutex
	start             time.Time
	points            map[string]int64
	timeline          []chatPhaseProbePoint
	sseOutput         chan struct{}
	finished          chan struct{}
	sseOnce           sync.Once
	holdSyntheticBody bool
	requests          int
	requestBytes      int64
	requestBytesKnown bool
	outputBytes       int
	reasoningBytes    int
	toolCalls         int
	messageCount      int
	toolDefCount      int
	userSeq           int
	calls             []chatPhaseProbeCall
}

func (p *chatPhaseProbeTrace) mark(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ns := time.Since(p.start).Nanoseconds()
	if _, exists := p.points[name]; !exists {
		p.points[name] = ns
	}
	p.timeline = append(p.timeline, chatPhaseProbePoint{name, ns})
}

func (p *chatPhaseProbeTrace) point(name string) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.points[name]
}

func (p *chatPhaseProbeTrace) hasPoint(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, exists := p.points[name]
	return exists
}

func (p *chatPhaseProbeTrace) recordCall(s llm.CallStat) {
	p.mu.Lock()
	p.calls = append(p.calls, chatPhaseProbeCall{
		Purpose: s.Purpose, TTFTNS: s.TTFT.Nanoseconds(), Duration: s.Duration.Nanoseconds(),
		Input: s.TokensIn, Output: s.TokensOut, Cached: s.TokensCached, Reasoning: s.TokensReasoning,
		Canceled: s.Canceled, Failed: s.Failed,
	})
	p.mu.Unlock()
	p.mark("metered_call_recorded")
}

func chatPhaseProbeTraceFrom(ctx context.Context) *chatPhaseProbeTrace {
	p, _ := ctx.Value(chatPhaseProbeTraceKey{}).(*chatPhaseProbeTrace)
	return p
}

type chatPhaseProbeProvider struct{ inner llm.Provider }

func (p chatPhaseProbeProvider) Name() string         { return p.inner.Name() }
func (p chatPhaseProbeProvider) Unwrap() llm.Provider { return p.inner }

func (p chatPhaseProbeProvider) Complete(ctx context.Context, msgs []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	trace := chatPhaseProbeTraceFrom(ctx)
	if trace == nil {
		return p.inner.Complete(ctx, msgs, defs)
	}
	trace.mark("provider_entry")
	trace.mu.Lock()
	trace.messageCount = len(msgs)
	trace.toolDefCount = len(defs)
	trace.mu.Unlock()
	in, err := p.inner.Complete(ctx, msgs, defs)
	trace.mark("provider_return")
	if err != nil {
		close(trace.finished)
		return nil, err
	}
	// One extra test-only handoff marks delivery to the agent. Cancellation
	// waits for the later SSE write, because receipt of a Delta precedes
	// transcript processing and may still lose a race to ctx.Done().
	out := make(chan llm.Delta)
	go func() {
		defer close(out)
		defer close(trace.finished)
		defer trace.mark("provider_stream_end")
		handedOff := false
		for d := range in {
			modelOutput := d.Content != "" || d.Reasoning != "" || d.OutputStarted || d.ReasoningStarted || d.ToolCall != nil
			if modelOutput {
				trace.mark("provider_model_output")
				trace.mu.Lock()
				trace.outputBytes += len(d.Content)
				trace.reasoningBytes += len(d.Reasoning)
				if d.ToolCall != nil {
					trace.toolCalls++
				}
				trace.mu.Unlock()
			}
			select {
			case out <- d:
				if modelOutput && !handedOff {
					trace.mark("first_provider_handoff")
					handedOff = true
				}
			case <-ctx.Done():
				// Metered closes on cancellation without waiting for an
				// uncooperative raw producer. Observe its final accounting.
				for range in {
				}
				return
			}
		}
	}()
	return out, nil
}

type chatPhaseProbeSyntheticTransport struct{}

func (chatPhaseProbeSyntheticTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	trace := chatPhaseProbeTraceFrom(req.Context())
	if trace == nil {
		return nil, fmt.Errorf("synthetic phase request has no trace")
	}
	trace.mark("http_request_begin")
	count, err := io.Copy(io.Discard, req.Body)
	if err != nil {
		return nil, err
	}
	trace.mu.Lock()
	trace.requests++
	trace.requestBytes += count
	trace.requestBytesKnown = true
	trace.mu.Unlock()
	trace.mark("http_request_body_consumed")
	body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"synthetic partial\"}\n\n"
	if !trace.holdSyntheticBody {
		body = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"synthetic \"}\n\n" +
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"answer\"}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":128,\"output_tokens\":3,\"total_tokens\":131}}}\n\n"
	}
	trace.mark("http_response_headers")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       &chatPhaseProbeBody{reader: strings.NewReader(body), ctx: req.Context(), trace: trace},
		Request:    req,
	}, nil
}

type chatPhaseProbeBody struct {
	reader *strings.Reader
	ctx    context.Context
	trace  *chatPhaseProbeTrace
}

func (b *chatPhaseProbeBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	if n > 0 {
		b.trace.mark("http_response_body_read")
		return n, err
	}
	if b.trace.holdSyntheticBody {
		<-b.ctx.Done()
		b.trace.mark("http_body_cancel_observed")
		return 0, b.ctx.Err()
	}
	return n, err
}

func (b *chatPhaseProbeBody) Close() error {
	b.trace.mark("http_response_body_closed")
	return nil
}

type chatPhaseProbeWriter struct {
	*httptest.ResponseRecorder
	trace     *chatPhaseProbeTrace
	pending   bytes.Buffer
	err       error
	toolNames map[string]string
}

func (w *chatPhaseProbeWriter) WriteHeader(status int) {
	w.trace.mark("http_headers")
	w.ResponseRecorder.WriteHeader(status)
}

func (w *chatPhaseProbeWriter) Write(p []byte) (int, error) {
	_, _ = w.pending.Write(p)
	return w.ResponseRecorder.Write(p)
}

func (w *chatPhaseProbeWriter) WriteString(s string) (int, error) {
	// writeSSEFrame uses io.WriteString for the prefix and separator.
	// Override the recorder's promoted method so both reach the parser.
	return w.Write([]byte(s))
}

func (w *chatPhaseProbeWriter) Flush() {
	w.ResponseRecorder.Flush()
	frames := strings.Split(w.pending.String(), "\n\n")
	w.pending.Reset()
	_, _ = w.pending.WriteString(frames[len(frames)-1])
	for _, frame := range frames[:len(frames)-1] {
		var event struct {
			Type    string `json:"type"`
			UserSeq int    `json:"user_seq"`
			Text    string `json:"text"`
			Name    string `json:"name"`
			ID      string `json:"id"`
			Output  string `json:"output"`
			Err     string `json:"err"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(frame, "data:"))), &event); err != nil {
			w.err = err
			continue
		}
		w.trace.mark("sse_" + event.Type)
		if event.Type == "tool_call" {
			if w.toolNames == nil {
				w.toolNames = make(map[string]string)
			}
			w.toolNames[event.ID] = event.Name
		}
		if event.Type == "tool_result" {
			if w.toolNames[event.ID] == "web_download" && event.Err == "" && strings.HasPrefix(event.Output, "Download complete:") {
				w.trace.mark("successful_download")
			}
			delete(w.toolNames, event.ID)
		}
		if event.Type == "message" && event.Text != "" {
			w.trace.sseOnce.Do(func() {
				w.trace.mark("first_sse_output")
				close(w.trace.sseOutput)
			})
		}
		if event.Type == "session_activity" {
			w.trace.mu.Lock()
			w.trace.userSeq = event.UserSeq
			w.trace.mu.Unlock()
		}
	}
}

func chatPhaseProbeHTTPTrace(trace *chatPhaseProbeTrace) *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		GetConn: func(string) {
			trace.mark("http_request_begin")
			trace.mu.Lock()
			trace.requests++
			trace.mu.Unlock()
		},
		GotConn: func(info httptrace.GotConnInfo) {
			trace.mark("http_connection_ready")
			if info.Reused {
				trace.mark("http_connection_reused")
			}
		},
		WroteHeaderField: func(key string, values []string) {
			if strings.EqualFold(key, "Content-Length") && len(values) == 1 {
				if count, err := strconv.ParseInt(values[0], 10, 64); err == nil {
					trace.mu.Lock()
					trace.requestBytes += count
					trace.requestBytesKnown = true
					trace.mu.Unlock()
				}
			}
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				trace.mark("http_request_written")
			}
		},
		// First HTTP response byte includes headers; this is not a first
		// body byte, a first model token, or a pure prefill measurement.
		GotFirstResponseByte: func() { trace.mark("http_first_response_byte") },
	}
}

func chatPhaseProbeLocalRequest(method, path string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, path, body)
	r.Host = "127.0.0.1:8765"
	r.RemoteAddr = "127.0.0.1:1234"
	return r
}

func chatPhaseProbeRoot(tb testing.TB) string {
	tb.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		tb.Fatal(err)
	}
	root := filepath.Clean(filepath.Join(cwd, "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		tb.Fatalf("phase probe must run from internal/webgui: %v", err)
	}
	parent := filepath.Join(root, ".tmp", "optimization-oct8-2026", "closure-audit", "phase-probe-data")
	if err := os.MkdirAll(parent, 0700); err != nil {
		tb.Fatal(err)
	}
	dir, err := os.MkdirTemp(parent, "run-")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			tb.Errorf("remove phase fixture: %v", err)
		}
	})
	return dir
}

func chatPhaseProbeEngine(tb testing.TB, liveURL, model string) (*Server, http.Handler, int64) {
	tb.Helper()
	dir := chatPhaseProbeRoot(tb)
	home, data := filepath.Join(dir, "workspace"), filepath.Join(dir, "data")
	for _, path := range []string{home, data} {
		if err := os.MkdirAll(path, 0700); err != nil {
			tb.Fatal(err)
		}
	}
	// Single foreground call, no helper inference or subprocess preflight;
	// the production registry/prompt/memory path remains.
	if err := os.WriteFile(filepath.Join(data, "config.toml"), []byte("navigator = \"off\"\nmax_steps = 1\npreflight_repo = false\n"), 0600); err != nil {
		tb.Fatal(err)
	}
	cfg := echoConfig()
	if liveURL != "" {
		cfg.Provider, cfg.Model, cfg.BaseURL = config.ProviderOpenAI, model, liveURL
		cfg.APIKey, cfg.MaxTokens, cfg.Timeout = "", 64, 60*time.Second
		if err := cfg.Normalize(); err != nil {
			tb.Fatal(err)
		}
	}
	start := time.Now()
	eng, err := NewEngine(cfg, home, data)
	if err != nil {
		tb.Fatal(err)
	}
	startup := time.Since(start).Nanoseconds()
	tb.Cleanup(func() { _ = eng.Close() })
	if liveURL == "" {
		provider, err := llm.NewResponses(llm.ResponsesConfig{
			BaseURL: "https://phase-fixture.invalid/v1", Model: cfg.Model,
			HTTPClient: &http.Client{Transport: chatPhaseProbeSyntheticTransport{}},
		})
		if err != nil {
			tb.Fatal(err)
		}
		eng.prov = llm.Metered(provider, "synthetic-responses", llm.PurposeMain, func(llm.CallStat) {})
	}
	eng.prov = chatPhaseProbeProvider{inner: eng.prov}
	srv := NewServer(eng, false)
	return srv, srv.Handler(), startup
}

type chatPhaseProbeResult struct {
	Mode              string                `json:"mode"`
	Label             string                `json:"label"`
	UserSeq           int                   `json:"user_seq"`
	RequestCount      int                   `json:"http_request_count"`
	RequestBytes      int64                 `json:"request_payload_bytes"`
	RequestKnown      bool                  `json:"request_payload_bytes_known"`
	OutputBytes       int                   `json:"output_text_bytes"`
	ReasoningBytes    int                   `json:"output_reasoning_bytes"`
	ToolCalls         int                   `json:"tool_fragments"`
	MessageCount      int                   `json:"provider_messages"`
	ToolDefCount      int                   `json:"provider_tool_definitions"`
	Intervals         map[string]int64      `json:"intervals_ns"`
	Points            map[string]int64      `json:"points_ns"`
	Timeline          []chatPhaseProbePoint `json:"timeline"`
	Calls             []chatPhaseProbeCall  `json:"model_calls"`
	Phases            map[string]int64      `json:"persisted_phases_us"`
	SummaryDuration   int64                 `json:"persisted_turn_duration_ms"`
	SummaryModelCalls int                   `json:"persisted_model_calls"`
}

func (p *chatPhaseProbeTrace) result(mode, label string) chatPhaseProbeResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	points := make(map[string]int64, len(p.points))
	for key, value := range p.points {
		points[key] = value
	}
	return chatPhaseProbeResult{
		Mode: mode, Label: label, UserSeq: p.userSeq,
		RequestCount: p.requests, RequestBytes: p.requestBytes, RequestKnown: p.requestBytesKnown,
		OutputBytes: p.outputBytes, ReasoningBytes: p.reasoningBytes, ToolCalls: p.toolCalls,
		MessageCount: p.messageCount, ToolDefCount: p.toolDefCount,
		Intervals: chatPhaseProbeIntervals(points), Points: points, Timeline: append([]chatPhaseProbePoint(nil), p.timeline...),
		Calls: append([]chatPhaseProbeCall(nil), p.calls...),
	}
}

func chatPhaseProbeIntervals(points map[string]int64) map[string]int64 {
	intervals := make(map[string]int64)
	for _, pair := range [][3]string{
		{"submit_to_headers", "server_submit", "http_headers"},
		{"headers_to_session_state_ready", "http_headers", "sse_session"},
		{"session_to_provider_entry", "sse_session", "provider_entry"},
		{"provider_entry_to_handoff", "provider_entry", "provider_return"},
		{"provider_entry_to_http_request", "provider_entry", "http_request_begin"},
		{"http_request_to_body_consumed_synthetic", "http_request_begin", "http_request_body_consumed"},
		{"http_request_to_written_live", "http_request_begin", "http_request_written"},
		{"http_written_to_first_response_byte_live", "http_request_written", "http_first_response_byte"},
		{"http_request_to_first_model_output", "http_request_begin", "provider_model_output"},
		{"first_model_output_to_first_message_sse", "provider_model_output", "sse_message"},
		{"first_model_output_to_finishing", "provider_model_output", "sse_finishing"},
		{"finishing_to_done_sse", "sse_finishing", "sse_done"},
		{"finishing_to_error_sse", "sse_finishing", "sse_error"},
		{"done_sse_to_handler_return", "sse_done", "handler_return"},
		{"error_sse_to_handler_return", "sse_error", "handler_return"},
		{"handler_return_to_receipt_return", "handler_return", "receipt_return"},
		{"submit_to_durable_receipt", "server_submit", "receipt_return"},
		{"cancel_to_durable_receipt", "cancel_request", "receipt_return"},
	} {
		from, fromOK := points[pair[1]]
		to, toOK := points[pair[2]]
		if fromOK && toOK {
			intervals[pair[0]] = to - from
		}
	}
	return intervals
}

func chatPhaseProbeTurn(t *testing.T, handler http.Handler, parent context.Context, prompt, sid, id string, stop, live bool) (*chatPhaseProbeTrace, string) {
	t.Helper()
	body, err := json.Marshal(chatRequest{Prompt: prompt, SessionID: sid, TurnID: id})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	trace := &chatPhaseProbeTrace{points: make(map[string]int64), sseOutput: make(chan struct{}), finished: make(chan struct{}), holdSyntheticBody: stop}
	ctx = context.WithValue(ctx, chatPhaseProbeTraceKey{}, trace)
	ctx = llm.WithCallSink(ctx, trace.recordCall)
	if live {
		ctx = httptrace.WithClientTrace(ctx, chatPhaseProbeHTTPTrace(trace))
	}
	request := chatPhaseProbeLocalRequest(http.MethodPost, "/api/chat", bytes.NewReader(body)).WithContext(ctx)
	writer := &chatPhaseProbeWriter{ResponseRecorder: httptest.NewRecorder(), trace: trace}
	done := make(chan struct{})
	trace.start = time.Now()
	trace.mark("server_submit")
	go func() {
		defer close(done)
		handler.ServeHTTP(writer, request)
		trace.mark("handler_return")
	}()
	if stop {
		select {
		case <-trace.sseOutput:
		case <-done:
			t.Fatal("synthetic hold ended before SSE output cancellation barrier")
		case <-parent.Done():
			t.Fatal(parent.Err())
		}
		trace.mark("cancel_request")
		cancel()
	}
	select {
	case <-done:
	case <-parent.Done():
		t.Fatal(parent.Err())
	}
	if writer.Code != http.StatusOK || writer.err != nil {
		t.Fatalf("chat status=%d SSE parse=%v", writer.Code, writer.err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, chatPhaseProbeLocalRequest(http.MethodGet, "/api/chat/completion?id="+id, nil).WithContext(parent))
	trace.mark("receipt_return")
	var receipt struct {
		SessionID string `json:"session_id"`
		Accepted  bool   `json:"accepted"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &receipt) != nil || !receipt.Accepted || receipt.SessionID == "" {
		t.Fatalf("missing durable receipt: status=%d", rec.Code)
	}
	if sid != "" && receipt.SessionID != sid {
		t.Fatal("receipt changed the conversation")
	}
	return trace, receipt.SessionID
}

// LIVE uses exactly two short foreground turns. Cancellation stays synthetic:
// a one-token OK can legitimately finish before a live abort can be scheduled.
func TestChatSyntheticPhaseProbe(t *testing.T) {
	liveURL := strings.TrimSpace(os.Getenv("SUPERCLI_PHASE_PROBE_LIVE_URL"))
	if os.Getenv("SUPERCLI_PHASE_PROBE") != "1" && liveURL == "" {
		t.Skip("opt-in phase measurement; no model request in the normal suite")
	}
	mode, prompt := "synthetic-responses", "hello"
	model := ""
	if liveURL != "" {
		model = strings.TrimSpace(os.Getenv("SUPERCLI_PHASE_PROBE_MODEL"))
		u, err := url.Parse(liveURL)
		if err != nil || u.User != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") || u.RawQuery != "" || u.Fragment != "" || model == "" {
			t.Fatal("LIVE requires an explicit loopback http URL without credentials and a model")
		}
		mode, prompt = "live-openai-factory", "Odpowiedz dokładnie OK bez narzędzi."
		previousEffort, previousThinking := llm.ReasoningEffort(), llm.ThinkingEnabled()
		if err := llm.SetReasoningEffort("none"); err != nil {
			t.Fatal(err)
		}
		llm.SetThinkingEnabled(false)
		t.Cleanup(func() {
			_ = llm.SetReasoningEffort(previousEffort)
			llm.SetThinkingEnabled(previousThinking)
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	srv, handler, startup := chatPhaseProbeEngine(t, liveURL, model)
	cold, sid := chatPhaseProbeTurn(t, handler, ctx, prompt, "", "phase-cold", false, liveURL != "")
	warm, _ := chatPhaseProbeTurn(t, handler, ctx, prompt, sid, "phase-warm", false, liveURL != "")
	traces, labels := []*chatPhaseProbeTrace{cold, warm}, []string{"cold_engine_first_session", "warm_engine_same_session"}
	stopToNext := int64(0)
	if liveURL == "" {
		stopped, _ := chatPhaseProbeTurn(t, handler, ctx, prompt, sid, "phase-cancel", true, false)
		next, _ := chatPhaseProbeTurn(t, handler, ctx, prompt, sid, "phase-next", false, false)
		stopToNext = next.start.Sub(stopped.start).Nanoseconds() + next.point("provider_entry") - stopped.point("cancel_request")
		traces = append(traces, stopped, next)
		labels = append(labels, "cancel_after_sse_output", "next_after_durable_receipt")
	}
	// Cancellation may let the agent finish before Metered records its last
	// counters. Wait only after the measured Stop→next sequence, so accounting
	// inspection does not extend either request or the completion fence.
	for _, trace := range traces {
		if !trace.hasPoint("provider_entry") {
			t.Fatalf("chat did not enter the measured provider; points=%v", trace.result(mode, "provider_missing").Points)
		}
		select {
		case <-trace.finished:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	// Inspect stored counters AFTER Stop→next turn so inspection cannot
	// manufacture latency between those two requests.
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	summaries, err := store.ReadRecentTurnTelemetry(ctx, time.Time{}, 32)
	if err != nil {
		t.Fatal(err)
	}
	results := make([]chatPhaseProbeResult, len(traces))
	var validationErrors []string
	for i, trace := range traces {
		results[i] = trace.result(mode, labels[i])
		upperSeq := int(^uint(0) >> 1)
		if i+1 < len(traces) {
			upperSeq = traces[i+1].userSeq
		}
		for _, summary := range summaries {
			if summary.SessionID == sid && summary.AssistantSeq > results[i].UserSeq && summary.AssistantSeq < upperSeq {
				results[i].Phases = summary.Phases
				results[i].SummaryDuration = summary.DurationMS
				results[i].SummaryModelCalls = summary.ModelCalls
				break
			}
		}
		// A recorded timestamp may legally be zero on a coarse clock.
		// result copied this map under the trace mutex; inspect key presence.
		var missing []string
		for _, name := range []string{"sse_session", "sse_session_activity", "provider_entry", "provider_model_output", "receipt_return"} {
			if _, exists := results[i].Points[name]; !exists {
				missing = append(missing, name)
			}
		}
		if len(missing) != 0 || results[i].UserSeq <= 0 {
			validationErrors = append(validationErrors, fmt.Sprintf("incomplete trace %s: missing=%v user_seq=%d points=%v", labels[i], missing, results[i].UserSeq, results[i].Points))
		}
		if len(results[i].Calls) != 1 || results[i].Calls[0].Purpose != llm.PurposeMain {
			validationErrors = append(validationErrors, fmt.Sprintf("%s expected one foreground model call: %+v", labels[i], results[i].Calls))
		}
		if results[i].RequestCount != 1 {
			validationErrors = append(validationErrors, fmt.Sprintf("%s HTTP request count=%d, expected exactly one", labels[i], results[i].RequestCount))
		}
	}
	if liveURL == "" {
		messages, err := store.ReadMessages(ctx, sid)
		if err != nil {
			t.Fatal(err)
		}
		partial := 0
		for _, message := range messages {
			if message.Role == string(llm.RoleAssistant) && webCapsuleText(message) == "synthetic partial" {
				partial++
			}
		}
		canceled := len(results[2].Calls) == 1 && results[2].Calls[0].Canceled
		if partial != 1 || !canceled {
			validationErrors = append(validationErrors, fmt.Sprintf("cancellation lost accepted output/accounting: partial=%d canceled=%t", partial, canceled))
		}
	}
	report := struct {
		Mode          string                 `json:"mode"`
		Model         string                 `json:"model,omitempty"`
		EngineStartup int64                  `json:"engine_startup_ns_outside_request"`
		StopToNext    int64                  `json:"cancel_to_next_provider_ns,omitempty"`
		Results       []chatPhaseProbeResult `json:"results"`
	}{mode, model, startup, stopToNext, results}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("phase-probe-result=%s", encoded)
	if len(validationErrors) != 0 {
		t.Fatal(strings.Join(validationErrors, "\n"))
	}
}

// Opt-in component benchmark, no model calls. It measures the production
// helpers separately rather than guessing their shares from overall TTFT.
func BenchmarkChatSyntheticSetupPhases(b *testing.B) {
	if os.Getenv("SUPERCLI_PHASE_PROBE") != "1" || os.Getenv("SUPERCLI_PHASE_PROBE_LIVE_URL") != "" {
		b.Skip("synthetic opt-in component benchmark")
	}
	srv, _, _ := chatPhaseProbeEngine(b, "", "")
	ctx := context.Background()
	initial, _, sid, err := srv.eng.sessionState(ctx, "hello", "", srv.eng.Home())
	if err != nil {
		b.Fatal(err)
	}
	if _, err := srv.eng.newLoopWithSessionAt(initial, nil, srv.eng.Home()); err != nil {
		b.Fatal(err)
	}
	b.Run("session_state_warm", func(b *testing.B) {
		b.ReportAllocs()
		for n := 0; n < b.N; n++ {
			if _, _, _, err := srv.eng.sessionState(ctx, "hello", sid, srv.eng.Home()); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("registry_prompt_memory_loop_warm", func(b *testing.B) {
		b.ReportAllocs()
		for n := 0; n < b.N; n++ {
			if _, err := srv.eng.newLoopWithSessionAt(initial, nil, srv.eng.Home()); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("session_recall_warm", func(b *testing.B) {
		b.ReportAllocs()
		for n := 0; n < b.N; n++ {
			_ = srv.eng.webRelevantSessionMemory(ctx, srv.eng.Home(), "hello", sid, webSessionRecallTokens)
		}
	})
}
