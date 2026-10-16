package webgui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type blockedChatWriter struct {
	header           http.Header
	entered, release chan struct{}
	once             sync.Once
}

func (w *blockedChatWriter) Header() http.Header { return w.header }
func (w *blockedChatWriter) WriteHeader(int)     {}
func (w *blockedChatWriter) Flush()              {}
func (w *blockedChatWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	return len(p), nil
}

func TestChatSlowResponseDoesNotBlockGeneration(t *testing.T) {
	s := newTestServer(t, false)
	p := &syncTestProvider{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	s.eng.prov = p
	writer := &blockedChatWriter{header: make(http.Header), entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(writer.release) }) }
	t.Cleanup(func() { s.eng.stopChatRuns(); unblock(); waitCompletionTest(t, done, "slow writer cleanup") })
	go func() {
		defer close(done)
		s.handleChat(writer, httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"prompt":"slow client","turn_id":"slow-client"}`)))
	}()
	waitCompletionTest(t, writer.entered, "blocked response")
	waitCompletionTest(t, p.entered, "unblocked generation")
	close(p.release)
	s.chatCompletions.Lock()
	receipt := s.chatCompletions.runs["slow-client"]
	s.chatCompletions.Unlock()
	waitCompletionTest(t, receipt.done, "completion despite blocked response")
	if s.eng.sessionRunActive(receipt.sessionID) {
		t.Fatal("slow response retained gate")
	}
	unblock()
	waitCompletionTest(t, done, "HTTP completion")
}
