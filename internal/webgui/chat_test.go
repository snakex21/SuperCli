package webgui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleChat_RejectsGet(t *testing.T) {
	srv := newTestServer(t, false)
	req := httptest.NewRequest(http.MethodGet, "/api/chat", nil)
	rec := httptest.NewRecorder()
	srv.handleChat(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/chat: status = %d, want 405", rec.Code)
	}
}

func TestHandleChat_BadJSON(t *testing.T) {
	srv := newTestServer(t, false)
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader("{not json"))
	rec := httptest.NewRecorder()
	srv.handleChat(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad json: status = %d, want 400", rec.Code)
	}
}

func TestHandleChat_EmptyPrompt(t *testing.T) {
	srv := newTestServer(t, false)
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"prompt":"   "}`))
	rec := httptest.NewRecorder()
	srv.handleChat(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty prompt: status = %d, want 400", rec.Code)
	}
}

func TestHandleChat_StreamsEcho(t *testing.T) {
	srv := newTestServer(t, false)
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"prompt":"hello"}`))
	rec := httptest.NewRecorder()
	srv.handleChat(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	body := rec.Body.String()
	// Every frame is an SSE data line carrying a JSON wire event.
	if !strings.Contains(body, "data: ") {
		t.Errorf("no SSE frames in body: %q", body)
	}
	// The echo run must terminate with a done event.
	if !strings.Contains(body, `"type":"done"`) {
		t.Errorf("stream missing done event: %q", body)
	}
	// Echo emits its prefix and body as separate provider deltas. They should
	// cross the HTTP boundary as one text frame, followed by the immediate
	// terminal frame.
	if got := strings.Count(body, `"type":"message"`); got != 1 {
		t.Errorf("message SSE frames = %d, want 1: %q", got, body)
	}
}

func TestHandleChat_RewindFeedbackReachesModelWithoutExtraCall(t *testing.T) {
	srv := newTestServer(t, false)
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(
		`{"prompt":"try again","rewound":true,"rewind_reason":"wrong file was changed","rewind_files":true}`))
	rec := httptest.NewRecorder()
	srv.handleChat(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "rewind_feedback") || !strings.Contains(body, "wrong file was changed") ||
		!strings.Contains(body, "restored the affected workspace files") {
		t.Fatalf("model response did not receive rewind feedback: %s", body)
	}
	if got := strings.Count(body, `"type":"done"`); got != 1 {
		t.Fatalf("done events = %d, want one model run: %s", got, body)
	}
}
