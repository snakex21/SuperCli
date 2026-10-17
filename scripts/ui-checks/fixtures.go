package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"
)

type requestGate struct {
	started, released      chan struct{}
	startOnce, releaseOnce sync.Once
	fail                   bool
}

func newGate() *requestGate {
	return &requestGate{started: make(chan struct{}), released: make(chan struct{})}
}
func (g *requestGate) start()   { g.startOnce.Do(func() { close(g.started) }) }
func (g *requestGate) release() { g.releaseOnce.Do(func() { close(g.released) }) }
func (g *requestGate) wait(ctx context.Context) error {
	select {
	case <-g.started:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("expected fixture request did not arrive: %w", ctx.Err())
	}
}
func (g *requestGate) enter(ctx context.Context) bool {
	g.start()
	select {
	case <-g.released:
		return true
	case <-ctx.Done():
		return false
	}
}

type fixture struct {
	mu                                  sync.Mutex
	mode, project, model, configured    string
	sessions, rowsA, rowsB              []map[string]any
	calls                               []map[string]any
	holdModels, holdReasoning, holdList *requestGate
	gates                               []*requestGate
	chat, activity                      *requestGate
	awaitActivity                       bool
	listCount                           int
	assets                              map[string][]byte
	remote                              http.Handler
	server                              *httptest.Server
}

func newFixture(root, mode, remote string) (*fixture, error) {
	assets, err := assetSnapshot(root)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	stamp := func(t time.Time) string { return t.Format("2006-01-02T15:04:05.000Z") }
	f := &fixture{mode: mode, project: "A", model: "qwen", configured: "low", assets: assets, chat: newGate(), activity: newGate()}
	f.sessions = []map[string]any{
		{"id": "a", "model": "qwen", "provider": "local", "reasoning_effort": "low", "runtime_known": true, "first_user_msg": "Lokalny model", "started_at": stamp(now), "message_count": 0},
		{"id": "b", "model": "gpt-5.6-sol", "provider": "cloud", "reasoning_effort": "medium", "runtime_known": true, "first_user_msg": "Model chmurowy", "started_at": stamp(now), "message_count": 0},
	}
	f.rowsA = []map[string]any{
		{"id": "new", "first_user_msg": "Nowsza rozmowa", "started_at": stamp(now), "updated_at": stamp(now), "message_count": 2, "model": "fixture"},
		{"id": "old", "first_user_msg": "Starsza rozmowa — wracam do projektu", "started_at": stamp(now.Add(-10 * 24 * time.Hour)), "updated_at": stamp(now.Add(-10 * 24 * time.Hour)), "message_count": 2, "model": "fixture"},
	}
	f.rowsB = []map[string]any{{"id": "other", "first_user_msg": "Inny projekt", "started_at": stamp(now), "updated_at": stamp(now), "message_count": 1}}
	if remote != "" {
		f.remote = proxyFor(remote)
	}
	f.server, err = loopbackServer(http.HandlerFunc(f.serveHTTP))
	return f, err
}
func (f *fixture) close() {
	f.mu.Lock()
	for _, g := range f.gates {
		g.release()
	}
	f.chat.release()
	f.activity.release()
	f.mu.Unlock()
	f.server.CloseClientConnections()
	f.server.Close()
}
func (f *fixture) hold(kind string, fail bool) *requestGate {
	g := newGate()
	g.fail = fail
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gates = append(f.gates, g)
	switch kind {
	case "models":
		f.holdModels = g
	case "reasoning":
		f.holdReasoning = g
	case "list":
		f.holdList = g
	}
	return g
}
func (f *fixture) setProject(project string) { f.mu.Lock(); f.project = project; f.mu.Unlock() }
func (f *fixture) reasoning() map[string]any {
	toggle := f.model == "qwen"
	effective := f.configured
	if toggle && effective != "" && effective != "none" {
		effective = "on"
	}
	selected := effective
	if selected == "on" {
		selected = "high"
	}
	levels := []string{"none", "minimal", "low", "medium", "high", "xhigh"}
	if toggle {
		levels = []string{"none", "high"}
	}
	return map[string]any{"configured": f.configured, "effective": effective, "selected": selected, "adjusted": effective != f.configured, "supported": true, "toggle_only": toggle, "levels": levels}
}
func defaultAPI() map[string]any {
	return map[string]any{"sessions": []any{}, "projects": []any{}, "providers": []any{}, "models": []any{}, "workers": []any{}, "tasks": []any{}, "settings": map[string]any{"ui.lang": "pl"}, "ui": map[string]any{"lang": "pl", "rememberSessionRuntime": false}}
}

func (f *fixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if scriptResponse(w, r) {
		return
	}
	if r.URL.Path == "/__ui_checks/state" {
		f.mu.Lock()
		defer f.mu.Unlock()
		jsonResponse(w, map[string]any{"sessions": f.sessions, "configured": f.configured, "model": f.model, "calls": f.calls, "listCount": f.listCount, "project": f.project})
		return
	}
	if f.remote != nil && (r.URL.Path == "/api/chat" || r.URL.Path == "/api/sync/stop" || len(r.URL.Path) >= 9 && r.URL.Path[:9] == "/fixture/") {
		f.remote.ServeHTTP(w, r)
		return
	}
	if len(r.URL.Path) < 5 || r.URL.Path[:5] != "/api/" {
		writeAsset(w, r, f.assets)
		return
	}
	if r.URL.Path == "/api/chat" && f.mode == "session-activity" {
		f.sessionChat(w, r)
		return
	}
	f.mu.Lock()
	var result any = defaultAPI()
	var held *requestGate
	switch r.URL.Path {
	case "/api/health":
		result = map[string]any{"ok": true, "chat_ready": true, "model": "fixture", "home": f.project}
	case "/api/transcript":
		result = map[string]any{"messages": []any{}, "has_more": false}
	case "/api/sessions":
		if f.mode == "reasoning-ui" {
			result = f.sessions
		}
		if f.mode == "session-activity" {
			f.listCount++
			rows := f.rowsA
			if f.project == "B" {
				rows = f.rowsB
			}
			if f.project == "A" && rows[1]["updated_at"].(string) > rows[0]["updated_at"].(string) {
				rows = []map[string]any{rows[1], rows[0]}
			}
			result = rows
			held = f.holdList
			f.holdList = nil
			if f.awaitActivity {
				f.awaitActivity = false
				f.activity.start()
			}
		}
	case "/api/models":
		if f.mode == "reasoning-ui" {
			provider := "cloud"
			if f.model == "qwen" {
				provider = "local"
			}
			result = map[string]any{"active": f.model, "provider": provider, "reasoning": f.reasoning(), "models": []any{}}
			held = f.holdModels
			f.holdModels = nil
		}
	case "/api/reasoning":
		if f.mode == "reasoning-ui" && r.Method == "GET" {
			result = f.reasoning()
		}
	}
	// Serialize the read snapshot before unlocking, including stale responses.
	encoded, _ := json.Marshal(result)
	f.mu.Unlock()
	if held != nil {
		// Send no-store headers before holding a GET. Chromium otherwise may
		// serialize a second identical request behind its unfinished headers.
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if held.fail {
			w.WriteHeader(http.StatusInternalServerError)
		}
		w.(http.Flusher).Flush()
		if !held.enter(r.Context()) {
			return
		}
		if held.fail {
			_, _ = w.Write([]byte("old request failed"))
			return
		}
	}
	if f.mode == "reasoning-ui" && r.Method == "POST" && (r.URL.Path == "/api/reasoning" || r.URL.Path == "/api/model") {
		var body map[string]any
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		f.mu.Lock()
		call := map[string]any{"path": r.URL.Path}
		for key, value := range body {
			call[key] = value
		}
		f.calls = append(f.calls, call)
		held = f.holdReasoning
		if r.URL.Path == "/api/reasoning" {
			f.holdReasoning = nil
		} else {
			held = nil
		}
		f.mu.Unlock()
		if held != nil && !held.enter(r.Context()) {
			return
		}
		f.mu.Lock()
		if r.URL.Path == "/api/model" {
			f.model, _ = body["model"].(string)
			result = map[string]any{}
		} else {
			f.configured, _ = body["level"].(string)
			if f.configured == "default" {
				f.configured = ""
			}
			state := f.reasoning()
			for _, saved := range f.sessions {
				if saved["id"] == body["session_id"] {
					saved["reasoning_effort"] = f.configured
					saved["model"] = f.model
					provider := "cloud"
					if f.model == "qwen" {
						provider = "local"
					}
					saved["provider"] = provider
					state["session"] = saved
				}
			}
			result = state
		}
		encoded, _ = json.Marshal(result)
		f.mu.Unlock()
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(encoded)
}

func (f *fixture) sessionChat(w http.ResponseWriter, r *http.Request) {
	var input struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.SessionID != "old" {
		http.Error(w, "expected old session", 400)
		return
	}
	f.mu.Lock()
	f.rowsA[1]["updated_at"] = time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	f.rowsA[1]["message_count"] = 3
	f.awaitActivity = true
	f.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	emit := func(event any) {
		data, _ := json.Marshal(event)
		fmt.Fprintf(w, "data: %s\n\n", data)
		w.(http.Flusher).Flush()
	}
	emit(map[string]any{"type": "session", "session_id": "old"})
	emit(map[string]any{"type": "session_activity", "session_id": "old"})
	if !f.chat.enter(r.Context()) {
		return
	}
	emit(map[string]any{"type": "message", "text": "Gotowe."})
	emit(map[string]any{"type": "done", "tok_in": 1, "tok_out": 1, "tok_total": 2})
}
