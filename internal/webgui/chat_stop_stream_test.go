package webgui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"supercli/internal/llm"
	"sync"
	"testing"
	"time"
)

type completionOpenStreamProvider struct{ entered, canceled, release chan struct{} }

func (p *completionOpenStreamProvider) Name() string { return "completion-open-stream" }
func (p *completionOpenStreamProvider) Complete(ctx context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	out := make(chan llm.Delta)
	go func() {
		defer close(out)
		for _, delta := range []llm.Delta{{Content: "partial answer"}, {Usage: &llm.Usage{Input: 20, Output: 3}}, {Notice: "accepted stream barrier"}} {
			select {
			case out <- delta:
			case <-ctx.Done():
				close(p.entered)
				close(p.canceled)
				<-p.release
				return
			}
		}
		close(p.entered)
		<-ctx.Done()
		close(p.canceled)
		<-p.release
	}()
	return out, nil
}
func TestChatStopCompletesWhileCanceledProducerChannelRemainsOpen(t *testing.T) {
	srv := newTestServer(t, false)
	if _, err := srv.eng.sessionStore(); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.eng.newLoop(); err != nil {
		t.Fatal(err)
	}
	provider := &completionOpenStreamProvider{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	srv.eng.mu.Lock()
	srv.eng.prov = provider
	srv.eng.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	release := func() { once.Do(func() { close(provider.release) }) }
	chatDone := make(chan struct{})
	t.Cleanup(func() { cancel(); release(); waitCompletionTest(t, chatDone, "open stream cleanup") })
	request := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"prompt":"hello","turn_id":"cancel-open-stream"}`)).WithContext(ctx)
	go func() { defer close(chatDone); srv.handleChat(httptest.NewRecorder(), request) }()
	waitCompletionTest(t, provider.entered, "stream barrier")
	started := time.Now()
	cancel()
	waitCompletionTest(t, provider.canceled, "stream cancellation")
	waitCompletionTest(t, chatDone, "receipt without producer close")
	elapsed := time.Since(started)
	recorder := httptest.NewRecorder()
	srv.handleChatCompletion(recorder, httptest.NewRequest(http.MethodGet, "/api/chat/completion?id=cancel-open-stream", nil))
	var receipt struct {
		SessionID string `json:"session_id"`
		Accepted  bool   `json:"accepted"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if !receipt.Accepted || receipt.SessionID == "" {
		t.Fatalf("lost receipt: %+v", receipt)
	}
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	messages, err := store.ReadMessages(context.Background(), receipt.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range messages {
		if m.Role == "assistant" && strings.Contains(webCapsuleText(m), "partial answer") {
			found = true
		}
	}
	storedSession, err := store.Get(receipt.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if storedSession.TokenIn != 20 || storedSession.TokenOut != 3 {
		t.Fatalf("Stop lost already-reported usage: %+v", storedSession)
	}
	if !found {
		t.Fatalf("Stop discarded the accepted partial answer: %+v", messages)
	}
	t.Logf("durable Stop completed in %s without waiting for producer EOF", elapsed)
}
