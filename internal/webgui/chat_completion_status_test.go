package webgui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"supercli/internal/llm"
)

func TestChatCompletionStatusObservesWithoutWaitingOrConsuming(t *testing.T) {
	s := &Server{}
	run, err := s.registerChatCompletion("status-turn", func() {})
	if err != nil {
		t.Fatal(err)
	}
	read := func(accepted, done bool, session string) {
		t.Helper()
		rec := httptest.NewRecorder()
		s.handleChatCompletion(rec, httptest.NewRequest(http.MethodGet, "/api/chat/completion?id=status-turn&status=1", nil))
		var got struct {
			TurnID    string `json:"turn_id"`
			SessionID string `json:"session_id"`
			Accepted  bool   `json:"accepted"`
			Done      bool   `json:"done"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if rec.Code != 200 || got.TurnID != "status-turn" || got.Accepted != accepted || got.Done != done || got.SessionID != session {
			t.Fatalf("status: %d %s", rec.Code, rec.Body.String())
		}
		if s.chatCompletions.runs["status-turn"] != run {
			t.Fatal("status consumed receipt")
		}
	}
	read(false, false, "")
	s.chatCompletions.Lock()
	run.accepted = true
	run.sessionID = "fresh"
	s.chatCompletions.Unlock()
	read(true, false, "fresh")
	close(run.done)
	read(true, true, "fresh")
	legacy := httptest.NewRecorder()
	s.handleChatCompletion(legacy, httptest.NewRequest(http.MethodGet, "/api/chat/completion?id=status-turn", nil))
	if legacy.Code != 200 || !strings.Contains(legacy.Body.String(), `"accepted":true`) {
		t.Fatal(legacy.Body.String())
	}
	if s.chatCompletions.runs["status-turn"] != nil {
		t.Fatal("legacy wait no longer consumes receipt")
	}
	missing := httptest.NewRecorder()
	s.handleChatCompletion(missing, httptest.NewRequest(http.MethodGet, "/api/chat/completion?id=missing&status=1", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing receipt became acceptance: %d", missing.Code)
	}
}

// Drop everything including the initial HTTP status/flush. Cancellation here is
// delivery-only: the real handler, durable insert, provider and sync hub run on.
type lostHeadersWriter struct {
	header http.Header
	cancel context.CancelFunc
	once   sync.Once
}

func (w *lostHeadersWriter) Header() http.Header       { return w.header }
func (w *lostHeadersWriter) WriteHeader(int)           {}
func (w *lostHeadersWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func (w *lostHeadersWriter) Flush()                    { w.once.Do(w.cancel) }

type lostHeadersProvider struct {
	syncTestProvider
	mutations atomic.Int32
	path      string
}

func (p *lostHeadersProvider) Complete(ctx context.Context, messages []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	p.mutations.Add(1)
	f, err := os.OpenFile(p.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	_, err = f.WriteString("mutation\n")
	f.Close()
	if err != nil {
		return nil, err
	}
	return p.syncTestProvider.Complete(ctx, messages, defs)
}

func TestChatLostHeadersStatusFindsOneDurableTurnAndMutation(t *testing.T) {
	s := newTestServer(t, false)
	p := &lostHeadersProvider{syncTestProvider: syncTestProvider{entered: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{})}, path: filepath.Join(t.TempDir(), "mutation.txt")}
	s.eng.mu.Lock()
	s.eng.prov = p
	s.eng.mu.Unlock()
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(p.release) }); s.eng.stopChatRuns(); s.eng.chatLifetime.wg.Wait() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"prompt":"Perform the isolated fixture mutation once.","turn_id":"lost-headers","client_id":"original"}`)).WithContext(ctx)
	s.handleChat(&lostHeadersWriter{header: make(http.Header), cancel: cancel}, req)
	waitCompletionTest(t, p.entered, "provider after lost HTTP headers")
	if ctx.Err() == nil {
		t.Fatal("transport was not lost")
	}
	rec := httptest.NewRecorder()
	s.handleChatCompletion(rec, httptest.NewRequest(http.MethodGet, "/api/chat/completion?id=lost-headers&status=1", nil))
	var status struct {
		SessionID string `json:"session_id"`
		Accepted  bool   `json:"accepted"`
		Done      bool   `json:"done"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || !status.Accepted || status.Done || status.SessionID == "" {
		t.Fatalf("active receipt: %d %s", rec.Code, rec.Body.String())
	}
	api := httptest.NewServer(s.Handler())
	defer api.Close()
	replay := openSyncSSE(t, api, status.SessionID)
	defer replay.close()
	snapshot := readSyncUntil(t, replay, "snapshot")
	if len(snapshot.Runs) != 1 || snapshot.Runs[0].TurnID != "lost-headers" {
		t.Fatalf("not following original turn: %+v", snapshot.Runs)
	}
	durableReceipt := false
	for _, ev := range snapshot.Events {
		if ev.Type == "session_activity" && ev.UserSeq > 0 {
			durableReceipt = true
		}
	}
	if !durableReceipt {
		t.Fatal("sync did not replay durable user identity")
	}
	release.Do(func() { close(p.release) })
	readSyncUntil(t, replay, "run_finished")
	s.chatCompletions.Lock()
	receipt := s.chatCompletions.runs["lost-headers"]
	s.chatCompletions.Unlock()
	waitCompletionTest(t, receipt.done, "durable completion")
	store, err := s.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	messages, err := store.ReadMessages(context.Background(), status.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	users := 0
	for _, m := range messages {
		if m.Role == "user" {
			users++
		}
	}
	b, err := os.ReadFile(p.path)
	if err != nil {
		t.Fatal(err)
	}
	if users != 1 || p.mutations.Load() != 1 || string(b) != "mutation\n" {
		t.Fatalf("duplicate effect: users=%d mutations=%d file=%q", users, p.mutations.Load(), b)
	}
}
