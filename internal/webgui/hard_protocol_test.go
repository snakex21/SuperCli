package webgui

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/memory"
	"supercli/internal/tools"
)

func TestHardProtocolWebSSEPersistsTranscriptAndMemory(t *testing.T) {
	srv := newTestServer(t, false)
	events, response := hardPostChat(t, srv, `{"prompt":"hard-memory-kobalt-731"}`)
	hardAssertWireContract(t, events, "done")
	if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("content-type=%q", got)
	}

	sessionID := events[0].SessionID
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	messages, err := store.ReadMessages(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) < 2 || messages[0].Role != "user" || messages[len(messages)-1].Role != "assistant" {
		t.Fatalf("persisted transcript=%+v", messages)
	}
	joined := ""
	for _, message := range messages {
		joined += message.Content
	}
	if !strings.Contains(joined, "hard-memory-kobalt-731") {
		t.Fatalf("persisted transcript lost sentinel: %q", joined)
	}

	project, err := memory.OpenProjectStore(srv.eng.DataDir(), srv.eng.Home())
	if err != nil {
		t.Fatal(err)
	}
	defer project.Close()
	entry, err := project.Get("web-session-" + sessionID)
	if err != nil {
		t.Fatalf("cross-session capsule: %v", err)
	}
	if !strings.Contains(entry.Content, "hard-memory-kobalt-731") {
		t.Fatalf("cross-session capsule lost sentinel: %q", entry.Content)
	}
}

func TestHardProtocolAskUserSurvivesCleanUpstreamEOF(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"ask-hard","type":"function","function":{"name":"ask_user","arguments":"{\"question\":\"Choose\",\"options\":[{\"label\":\"A\"},{\"label\":\"B\"}]}"}}]}}]}`+"\n\n")
			return // deliberately no finish_reason and no [DONE]
		}
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":"continued after hard choice"}}]}`+"\n\n")
		// Deliberately close cleanly again; content must become an implicit stop.
	}))
	defer upstream.Close()

	dir := t.TempDir()
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	provider, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: upstream.URL, Model: "hard-openai"})
	if err != nil {
		t.Fatal(err)
	}
	eng.mu.Lock()
	eng.prov = provider
	eng.mu.Unlock()
	store, err := eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(dir, "hard-openai", "hard protocol question")
	if err != nil {
		t.Fatal(err)
	}

	var events []wireEvent
	err = eng.runStream(context.Background(), "ask deterministically", sess.ID, "", func(event wireEvent) {
		events = append(events, event)
		if event.Type == "question" && event.Question != nil {
			if answerErr := eng.answerQuestion(event.Question.ID, tools.AskAnswer{Selected: []string{"B"}}); answerErr != nil {
				t.Errorf("answer question: %v", answerErr)
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	hardAssertWireContract(t, events, "done")
	var questions, results int
	var visible strings.Builder
	for _, event := range events {
		switch event.Type {
		case "question":
			questions++
		case "tool_result":
			if strings.Contains(event.Output, "user selected: B") {
				results++
			}
		case "message":
			visible.WriteString(event.Text)
		}
	}
	if questions != 1 || results != 1 || !strings.Contains(visible.String(), "continued after hard choice") {
		t.Fatalf("question flow: questions=%d results=%d visible=%q events=%+v", questions, results, visible.String(), events)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("upstream calls=%d, want exactly 2", got)
	}
}

func TestHardProtocolBrokenUpstreamAlwaysEndsWithOneError(t *testing.T) {
	tests := []struct {
		name string
		wire string
	}{
		{"empty", ""},
		{"metadata only", "data: {\"id\":\"meta\",\"object\":\"chat.completion.chunk\",\"choices\":[]}\n\n"},
		{"malformed", "data: {broken-json}\n\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, tc.wire)
			}))
			defer upstream.Close()
			srv := newTestServer(t, false)
			provider, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: upstream.URL, Model: "hard-broken"})
			if err != nil {
				t.Fatal(err)
			}
			srv.eng.mu.Lock()
			srv.eng.prov = provider
			srv.eng.mu.Unlock()

			events, _ := hardPostChat(t, srv, `{"prompt":"must fail visibly"}`)
			hardAssertWireContract(t, events, "error")
			if strings.TrimSpace(events[len(events)-1].Err) == "" {
				t.Fatal("terminal error has no useful message")
			}
		})
	}
}

func TestHardProtocolPortableDraftRoundTrip(t *testing.T) {
	srv := newTestServer(t, false)
	body := `{"supercli-composer-drafts-v1":{"session:hard":{"text":"unfinished portable text","updated":731}}}`
	post := httptest.NewRecorder()
	srv.handleUISettings(post, httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(body)))
	if post.Code != http.StatusOK {
		t.Fatalf("save draft: status=%d body=%s", post.Code, post.Body.String())
	}
	settingsPath := filepath.Join(srv.eng.DataDir(), uiSettingsFile)
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "unfinished portable text") {
		t.Fatalf("portable settings lost draft: %s", data)
	}
	if filepath.Dir(settingsPath) != filepath.Clean(srv.eng.DataDir()) {
		t.Fatalf("settings escaped portable data dir: %q", settingsPath)
	}

	get := httptest.NewRecorder()
	srv.handleUISettings(get, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), "unfinished portable text") {
		t.Fatalf("load draft: status=%d body=%s", get.Code, get.Body.String())
	}
}

func hardPostChat(t *testing.T, srv *Server, body string) ([]wireEvent, *httptest.ResponseRecorder) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req.Host = "127.0.0.1:7777"
	req.RemoteAddr = "127.0.0.1:43110"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat status=%d body=%s", rec.Code, rec.Body.String())
	}
	return hardDecodeSSE(t, rec.Body.String()), rec
}

func hardDecodeSSE(t *testing.T, body string) []wireEvent {
	t.Helper()
	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var events []wireEvent
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			t.Fatalf("invalid SSE line %q", line)
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var event wireEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			t.Fatalf("invalid SSE JSON %q: %v", payload, err)
		}
		if strings.TrimSpace(event.Type) == "" {
			t.Fatalf("SSE event has no type: %s", payload)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan SSE: %v", err)
	}
	return events
}

func hardAssertWireContract(t *testing.T, events []wireEvent, wantTerminal string) {
	t.Helper()
	if len(events) < 2 {
		t.Fatalf("too few events: %+v", events)
	}
	if events[0].Type != "session" || strings.TrimSpace(events[0].SessionID) == "" {
		t.Fatalf("first event must establish session: %+v", events[0])
	}
	terminalCount := 0
	terminalIndex := -1
	for i, event := range events {
		if event.Type == "done" || event.Type == "error" {
			terminalCount++
			terminalIndex = i
		}
	}
	if terminalCount != 1 || terminalIndex != len(events)-1 {
		t.Fatalf("terminal contract: count=%d index=%d events=%+v", terminalCount, terminalIndex, events)
	}
	if events[terminalIndex].Type != wantTerminal {
		t.Fatalf("terminal=%q, want %q; err=%q", events[terminalIndex].Type, wantTerminal, events[terminalIndex].Err)
	}
}
