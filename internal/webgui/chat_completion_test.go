package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/llm"
)

type completionBarrierProvider struct {
	entered  chan struct{}
	canceled chan struct{}
	release  chan struct{}
}

func (p *completionBarrierProvider) Name() string { return "completion-barrier" }
func (p *completionBarrierProvider) Complete(ctx context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	close(p.entered)
	<-ctx.Done()
	close(p.canceled)
	<-p.release
	return nil, ctx.Err()
}

type completionWaitContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *completionWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}
func waitCompletionTest(t *testing.T, done <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting for %s", label)
	}
}
func TestChatCompletionWaitsForCanceledLoopAndRecoversFreshSession(t *testing.T) {
	srv := newTestServer(t, false)
	provider := &completionBarrierProvider{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	srv.eng.mu.Lock()
	srv.eng.prov = provider
	srv.eng.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(provider.release) }) }
	defer release()
	chatDone := make(chan struct{})
	request := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"prompt":"hello","turn_id":"test-turn-1"}`)).WithContext(ctx)
	go func() {
		defer close(chatDone)
		srv.handleChat(httptest.NewRecorder(), request)
	}()
	waitCompletionTest(t, provider.entered, "provider")
	cancel()
	waitCompletionTest(t, provider.canceled, "provider cancellation")
	srv.chatCompletions.Lock()
	receipt := srv.chatCompletions.runs["test-turn-1"]
	srv.chatCompletions.Unlock()
	if receipt == nil {
		t.Fatal("missing receipt")
	}
	select {
	case <-receipt.done:
		t.Fatal("completion published before the canceled loop returned")
	default:
	}
	waiterCtx := &completionWaitContext{Context: context.Background(), entered: make(chan struct{})}
	recorder := httptest.NewRecorder()
	completionDone := make(chan struct{})
	go func() {
		defer close(completionDone)
		srv.handleChatCompletion(recorder, httptest.NewRequest(http.MethodGet, "/api/chat/completion?id=test-turn-1", nil).WithContext(waiterCtx))
	}()
	waitCompletionTest(t, waiterCtx.entered, "receipt waiter")
	select {
	case <-completionDone:
		t.Fatal("completion endpoint returned while the provider was finishing")
	default:
	}
	release()
	waitCompletionTest(t, chatDone, "chat completion")
	waitCompletionTest(t, completionDone, "receipt response")
	var result struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.SessionID == "" {
		t.Fatal("lost fresh session after early Stop")
	}
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	messages, err := store.ReadMessages(context.Background(), result.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) == 0 || messages[0].Role != "user" || messages[0].Content != "hello" {
		t.Fatalf("receipt preceded durable user history: %+v", messages)
	}
	srv.chatCompletions.Lock()
	defer srv.chatCompletions.Unlock()
	if _, exists := srv.chatCompletions.runs["test-turn-1"]; exists {
		t.Fatal("consumed receipt retained")
	}
}
func TestChatCompletionRegistryIsBoundedAndPreservesActiveWaiters(t *testing.T) {
	srv := &Server{}
	for i := 0; i < chatCompletionLimit; i++ {
		if _, err := srv.registerChatCompletion(fmt.Sprintf("run-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := srv.registerChatCompletion("overflow"); err == nil {
		t.Fatal("unbounded active registry")
	}
	active := srv.chatCompletions.runs["run-0"]
	close(active.done)
	if _, err := srv.registerChatCompletion("replacement"); err != nil {
		t.Fatal(err)
	}
	if _, exists := srv.chatCompletions.runs["run-0"]; exists {
		t.Fatal("completed receipt not reclaimed")
	}
	if len(srv.chatCompletions.runs) != chatCompletionLimit {
		t.Fatal("incorrect registry size")
	}
	if _, err := srv.registerChatCompletion("run-1"); err == nil {
		t.Fatal("duplicate active turn accepted")
	}
	for _, id := range []string{"bad/id", strings.Repeat("x", 129)} {
		if _, err := srv.registerChatCompletion(id); err == nil {
			t.Fatalf("invalid id accepted: %q", id)
		}
	}
}
func TestChatCompletionCanceledWaitDoesNotRemoveActiveReceipt(t *testing.T) {
	srv := &Server{}
	receipt, err := srv.registerChatCompletion("active")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	srv.handleChatCompletion(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/chat/completion?id=active", nil).WithContext(ctx))
	if srv.chatCompletions.runs["active"] != receipt {
		t.Fatal("canceled waiter removed live receipt")
	}
	close(receipt.done)
	for _, tc := range []struct {
		method, url string
		status      int
	}{
		{http.MethodPost, "/api/chat/completion?id=active", http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/chat/completion?id=missing", http.StatusNotFound},
	} {
		rec := httptest.NewRecorder()
		srv.handleChatCompletion(rec, httptest.NewRequest(tc.method, tc.url, nil))
		if rec.Code != tc.status {
			t.Fatalf("status=%d want=%d", rec.Code, tc.status)
		}
	}
}

func TestChatCompletionDoesNotAcceptProviderSetupFailure(t *testing.T) {
	srv := newTestServer(t, false)
	srv.eng.SetAppProfile("nestcafe")
	rec := httptest.NewRecorder()
	srv.handleChat(rec, httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"prompt":"keep queued","turn_id":"setup-failed"}`)))
	result := httptest.NewRecorder()
	srv.handleChatCompletion(result, httptest.NewRequest(http.MethodGet, "/api/chat/completion?id=setup-failed", nil))
	var receipt struct {
		Accepted bool `json:"accepted"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Accepted {
		t.Fatal("setup failure accepted a prompt that never reached the loop")
	}
}
