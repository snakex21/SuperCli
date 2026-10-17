package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
