package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFixtureBackedChecksHeadless(t *testing.T) {
	for _, name := range []string{"delegation-ui", "reasoning-ui", "session-activity"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
			defer cancel()
			result, err := runCheck(ctx, options{Case: name, Out: t.TempDir()})
			if err != nil {
				t.Fatalf("%v\nmetrics=%v\nartifacts=%s", err, result.Metrics, result.Artifacts)
			}
			if !result.Passed || len(result.Checks) == 0 || len(result.Screenshots) == 0 {
				t.Fatalf("incomplete report: %+v", result)
			}
			t.Logf("%s: %d assertions, %d screenshots; %v", name, len(result.Checks), len(result.Screenshots), result.Metrics)
		})
	}
}

// These are explicitly owned synthetic servers, not external app integration.
// They also make Chrome parse/execute the remaining ESM check modules.
func TestExternalCasesOnOwnedAssetMock(t *testing.T) {
	root, err := repoRoot("")
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := newFixture(root, "isolated-assets", "")
	if err != nil {
		t.Fatal(err)
	}
	defer fixture.close()
	var mu sync.Mutex
	settings := map[string]any{"ui.lang": "pl", "ui.uiScale": "auto"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/settings" {
			fixture.serveHTTP(w, r)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "POST" {
			var values map[string]any
			if err := json.NewDecoder(r.Body).Decode(&values); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			for key, value := range values {
				settings[key] = value
			}
		}
		jsonResponse(w, map[string]any{"settings": settings})
	}))
	defer server.Close()
	defer server.CloseClientConnections()
	for _, name := range []string{"model-palette", "ui-scaling"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
			defer cancel()
			result, err := runCheck(ctx, options{Case: name, URL: server.URL, Out: t.TempDir()})
			if err != nil {
				t.Fatalf("%v\nmetrics=%v\nartifacts=%s", err, result.Metrics, result.Artifacts)
			}
			want := 6
			if name == "ui-scaling" {
				want = 12
			}
			if len(result.Checks) != want {
				t.Fatalf("assertions=%d, want %d: %+v", len(result.Checks), want, result)
			}
			t.Logf("%s: %d assertions on explicitly owned Go asset/settings mock; external integration not exercised", name, len(result.Checks))
		})
	}
}

// A Playwright locator re-resolved rows after background model refreshes. Native
// checks must do the same rather than measuring a detached, cached DOM node.
func TestModelPaletteRowRefresh(t *testing.T) {
	root, err := repoRoot("")
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := newFixture(root, "isolated-assets", "")
	if err != nil {
		t.Fatal(err)
	}
	defer fixture.close()
	refreshed := make(chan struct{}, 1)
	const refresh = `<script>
(() => {
  const list = document.querySelector('#model-list');
  const observer = new MutationObserver(() => {
    if (!list.querySelector('.prow')) return;
    observer.disconnect();
    setTimeout(() => {
      const top = list.scrollTop;
      list.replaceChildren(...Array.from(list.children, row => row.cloneNode(true)));
      list.scrollTop = top;
      fetch('/__palette_row_refresh', {method:'POST'});
    }, 60);
  });
  observer.observe(list, {childList:true, subtree:true});
})();
</script>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/__palette_row_refresh":
			select {
			case refreshed <- struct{}{}:
			default:
			}
			w.WriteHeader(http.StatusNoContent)
		case "/api/settings":
			jsonResponse(w, map[string]any{"settings": map[string]string{"ui.lang": "pl", "ui.uiScale": "auto"}})
		case "/":
			recorder := httptest.NewRecorder()
			fixture.serveHTTP(recorder, r)
			body := strings.Replace(recorder.Body.String(), "</body>", refresh+"</body>", 1)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, body)
		default:
			fixture.serveHTTP(w, r)
		}
	}))
	defer server.Close()
	defer server.CloseClientConnections()
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	result, err := runCheck(ctx, options{Case: "model-palette", URL: server.URL, Out: t.TempDir()})
	if err != nil {
		t.Fatalf("%v\nmetrics=%v", err, result.Metrics)
	}
	if !result.Passed || len(result.Checks) != 6 {
		t.Fatalf("incomplete palette check: %+v", result)
	}
	select {
	case <-refreshed:
	default:
		t.Fatal("fixture did not replace the model rows during the check")
	}
}

func TestExplicitExternalOriginsAndModes(t *testing.T) {
	for _, name := range []string{"model-palette", "ui-scaling", "chat-command-end-http", "chat-stop-http"} {
		if err := validate(options{Case: name}); err == nil || !strings.Contains(err.Error(), "explicit -url") {
			t.Fatalf("%s accepted implicit external URL: %v", name, err)
		}
	}
	for _, opts := range []options{
		{Case: "unknown"}, {Case: "delegation-ui", URL: "http://127.0.0.1:1"},
		{Case: "model-palette", URL: "file:///private"}, {Case: "model-palette", URL: "http://user:pass@127.0.0.1:1"},
		{Case: "model-palette", URL: "http://127.0.0.1:1", NestCafeURL: "http://127.0.0.1:2"},
		{Case: "chat-stop-http", URL: "http://localhost:123", Mode: "running-command"},
		{Case: "chat-stop-http", URL: "http://127.0.0.1:123", Mode: "bad"},
		{Case: "chat-command-end-http", URL: "http://127.0.0.1:123", Mode: "cmd-error", CallID: "bad"},
	} {
		if err := validate(opts); err == nil {
			t.Errorf("accepted invalid options: %+v", opts)
		}
	}
	var stdout, stderr bytes.Buffer
	if code := cli(t.Context(), []string{"-case", "model-palette"}, &stdout, &stderr); code != 2 || stdout.Len() != 0 {
		t.Fatalf("missing URL started a browser: code=%d out=%s err=%s", code, stdout.String(), stderr.String())
	}
}

// This mock verifies the native browser port's HTTP/SSE/UI contract without
// spawning cmd/curl, invoking an application engine, or contacting any model.
// Real backend execution/checkpoint integration remains the caller's job.
type chatMock struct {
	mu                 sync.Mutex
	kind, mode, callID string
	requests           []map[string]any
	active             bool
	stopOnce           sync.Once
	stopped            chan struct{}
	stopTurn           string
}

func (m *chatMock) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
		http.Error(w, "fixture Origin/Host mismatch", http.StatusForbidden)
		return
	}
	switch r.URL.Path {
	case "/fixture/active-work":
		m.mu.Lock()
		defer m.mu.Unlock()
		jsonResponse(w, map[string]any{"activeWork": m.active})
		return
	case "/fixture/command-ready":
		w.WriteHeader(204)
		return
	case "/fixture/command-canceled":
		select {
		case <-m.stopped:
			w.WriteHeader(204)
		case <-r.Context().Done():
		}
		return
	case "/api/sync/stop":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		m.mu.Lock()
		m.stopTurn, _ = body["turn_id"].(string)
		m.active = false
		m.mu.Unlock()
		m.stopOnce.Do(func() { close(m.stopped) })
		jsonResponse(w, map[string]any{"ok": true})
		return
	case "/api/chat":
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		m.mu.Lock()
		m.requests = append(m.requests, input)
		number := len(m.requests)
		m.active = true
		m.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		emit := func(event any) {
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "data: %s\n\n", data)
			w.(http.Flusher).Flush()
		}
		emit(map[string]any{"type": "session", "session_id": "native-browser-fixture"})
		if number == 1 {
			id := m.callID
			if m.kind == "chat-stop-http" {
				id = "blocked-curl"
			}
			emit(map[string]any{"type": "tool_call", "id": id, "name": "ctx_execute", "args": `{"command":["synthetic-only-no-process"]}`})
			if m.kind == "chat-stop-http" {
				<-r.Context().Done()
				return
			}
			emit(map[string]any{"type": "tool_result", "id": id, "output": "synthetic native result", "err": m.mode == "cmd-error"})
		} else {
			message := "next-command-end response"
			if m.kind == "chat-stop-http" {
				message = "after stop response"
			}
			emit(map[string]any{"type": "message", "text": message})
		}
		m.mu.Lock()
		m.active = false
		m.mu.Unlock()
		emit(map[string]any{"type": "done", "tok_in": 1, "tok_out": 1, "tok_total": 2})
		return
	}
	http.NotFound(w, r)
}

// Isolate the initial SSE response contract from Chrome/app preparation. The
// first frames must survive the real HTTP/1 proxy while the upstream handler
// remains blocked; waiting for EOF would turn this into a false success.
func TestChatMockInitialSSEThroughProxy(t *testing.T) {
	for _, chunked := range []bool{false, true} {
		t.Run(fmt.Sprintf("chunked-request=%t", chunked), func(t *testing.T) {
			mock := &chatMock{kind: "chat-stop-http", mode: "checkpoint-store-held", stopped: make(chan struct{})}
			upstream := httptest.NewServer(http.HandlerFunc(mock.serveHTTP))
			defer upstream.Close()
			defer upstream.CloseClientConnections()
			proxy := httptest.NewServer(proxyFor(upstream.URL))
			defer proxy.Close()
			defer proxy.CloseClientConnections()
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			const body = `{"prompt":"synthetic HTTP contract","turn_id":"initial-sse"}`
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, proxy.URL+"/api/chat", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			if chunked {
				request.Body = io.NopCloser(strings.NewReader(body))
				request.ContentLength = -1
			}
			request.Header.Set("Content-Type", "application/json")
			response, err := proxy.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
				t.Fatalf("SSE headers: status=%d headers=%v", response.StatusCode, response.Header)
			}
			reader := bufio.NewReader(response.Body)
			for _, want := range []string{"session", "tool_call"} {
				line, err := reader.ReadString('\n')
				if err != nil {
					t.Fatalf("SSE truncated before %s: %v", want, err)
				}
				var event struct{ Type, ID, Name string }
				data, ok := strings.CutPrefix(line, "data: ")
				if !ok || json.Unmarshal([]byte(data), &event) != nil || event.Type != want {
					t.Fatalf("expected %s frame, got %q", want, line)
				}
				if want == "tool_call" && (event.ID != "blocked-curl" || event.Name != "ctx_execute") {
					t.Fatalf("tool identity changed: %s", line)
				}
				if separator, err := reader.ReadString('\n'); err != nil || separator != "\n" {
					t.Fatalf("SSE frame delimiter: %q, %v", separator, err)
				}
			}
			mock.mu.Lock()
			active, requests := mock.active, len(mock.requests)
			mock.mu.Unlock()
			if !active || requests != 1 {
				t.Fatalf("initial stream already ended: active=%v requests=%d", active, requests)
			}
			select {
			case <-mock.stopped:
				t.Fatal("Stop occurred before receiving the first tool_call")
			default:
			}
		})
	}
}

// serverLikeBody models an incoming net/http request body: it delivers its
// bytes, and a later read waits until the handler starts the response, which
// is when net/http discards and closes an unread HTTP/1 body; that read then
// fails like the real one does.
type serverLikeBody struct {
	data      *strings.Reader
	ctx       context.Context
	started   <-chan struct{}
	exhausted bool
	lateRead  chan struct{}
	lateOnce  sync.Once
}

func (b *serverLikeBody) Read(p []byte) (int, error) {
	if !b.exhausted {
		n, _ := b.data.Read(p)
		if b.data.Len() == 0 {
			b.exhausted = true
			return n, io.EOF
		}
		return n, nil
	}
	b.lateOnce.Do(func() { close(b.lateRead) })
	select {
	case <-b.started:
		return 0, http.ErrBodyReadAfterClose
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	}
}

func (b *serverLikeBody) Close() error { return nil }

// streamRecorder is a flushing ResponseWriter that marks the response start.
type streamRecorder struct {
	mu        sync.Mutex
	header    http.Header
	body      bytes.Buffer
	status    int
	started   chan struct{}
	startOnce sync.Once
}

func (r *streamRecorder) Header() http.Header { return r.header }
func (r *streamRecorder) WriteHeader(code int) {
	r.mu.Lock()
	if r.status == 0 {
		r.status = code
	}
	r.mu.Unlock()
	r.startOnce.Do(func() { close(r.started) })
}
func (r *streamRecorder) Write(p []byte) (int, error) {
	r.WriteHeader(http.StatusOK)
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.Write(p)
}
func (r *streamRecorder) Flush() {}

// The proxy must not read the incoming request body after the streamed
// response has started: net/http closes it at that point, the transport then
// fails the forwarded request write and closes the upstream connection in the
// middle of the SSE body. That race truncated /api/chat before its first frame
// on loaded Linux CI ("network error" in Chrome). The body here forces that
// interleaving instead of relying on scheduling.
func TestProxyForwardsRequestBodyIndependentOfResponseStart(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input["turn_id"] != "body-race" {
			http.Error(w, fmt.Sprintf("forwarded body: %v %v", input, err), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"session\"}\n\n")
		w.(http.Flusher).Flush()
		fmt.Fprint(w, "data: {\"type\":\"done\"}\n\n")
	}))
	defer upstream.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	recorder := &streamRecorder{header: http.Header{}, started: make(chan struct{})}
	const payload = `{"prompt":"synthetic HTTP contract","turn_id":"body-race"}`
	body := &serverLikeBody{data: strings.NewReader(payload), ctx: ctx, started: recorder.started, lateRead: make(chan struct{})}
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "http://fixture.test/api/chat", body)
	request.ContentLength = int64(len(payload))
	request.Header.Set("Content-Type", "application/json")
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("proxy aborted the streamed response: %v", recovered)
			}
		}()
		proxyFor(upstream.URL).ServeHTTP(recorder, request)
	}()
	select {
	case <-body.lateRead:
		t.Fatal("proxy read the incoming request body after its bytes were consumed")
	default:
	}
	recorder.mu.Lock()
	status, text := recorder.status, recorder.body.String()
	recorder.mu.Unlock()
	if status != http.StatusOK || !strings.Contains(text, `"type":"session"`) || !strings.Contains(text, `"type":"done"`) {
		t.Fatalf("proxied SSE incomplete: status=%d body=%q", status, text)
	}
}

func TestChatPortsNativeHTTPMock(t *testing.T) {
	for _, spec := range []struct{ kind, mode string }{
		{"chat-command-end-http", "cmd-if-exist"}, {"chat-command-end-http", "curl-gif"}, {"chat-command-end-http", "cmd-error"}, {"chat-command-end-http", "checkpoint-rejected"},
		{"chat-stop-http", "running-command"}, {"chat-stop-http", "checkpoint-store-held"},
	} {
		t.Run(spec.kind+"/"+spec.mode, func(t *testing.T) {
			mock := &chatMock{kind: spec.kind, mode: spec.mode, callID: "command-end-" + spec.mode, stopped: make(chan struct{})}
			server := httptest.NewServer(http.HandlerFunc(mock.serveHTTP))
			defer server.Close()
			defer server.CloseClientConnections()
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			opts := options{Case: spec.kind, Mode: spec.mode, URL: server.URL, Out: t.TempDir()}
			if spec.kind == "chat-command-end-http" {
				opts.CallID = mock.callID
			}
			result, err := runCheck(ctx, opts)
			if err != nil {
				t.Fatalf("%v\nmetrics=%v\nartifacts=%s", err, result.Metrics, result.Artifacts)
			}
			mock.mu.Lock()
			defer mock.mu.Unlock()
			if len(mock.requests) != 2 {
				t.Fatalf("request count=%d", len(mock.requests))
			}
			if spec.kind == "chat-stop-http" && (mock.stopTurn == "" || mock.stopTurn != mock.requests[0]["turn_id"]) {
				t.Fatalf("Stop lost exact turn identity: stop=%q request=%v", mock.stopTurn, mock.requests[0])
			}
			t.Logf("%s %s: %d assertions, native HTTP/SSE; no real command/model; metrics=%v", spec.kind, spec.mode, len(result.Checks), result.Metrics)
		})
	}
}
