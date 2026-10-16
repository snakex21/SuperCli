package webgui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSyncSessionMetadataChanges(t *testing.T) {
	s := newTestServer(t, false)
	sub, _, done := s.eng.syncHub.subscribe("", "observer")
	defer done()
	s.handleChat(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"prompt":"metadata fixture"}`)))
	start := <-sub.events
	if start.Type != "run_started" || start.SessionID == "" {
		t.Fatal(start)
	}
	for len(sub.events) > 0 {
		<-sub.events
	}
	if err := s.eng.renameSession(start.SessionID, "renamed on phone"); err != nil {
		t.Fatal(err)
	}
	if frame := <-sub.events; frame.Type != "sessions_changed" {
		t.Fatal(frame)
	}
	if err := s.eng.deleteSession(start.SessionID); err != nil {
		t.Fatal(err)
	}
	if frame := <-sub.events; frame.Type != "sessions_changed" {
		t.Fatal(frame)
	}
}
