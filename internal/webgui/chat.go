package webgui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// chatRequest is the JSON body posted to /api/chat.
type chatRequest struct {
	Prompt    string `json:"prompt"`
	SessionID string `json:"session_id,omitempty"`
}

// handleChat runs one prompt and streams the agent's events back as
// Server-Sent Events. Each SSE "data:" line is one JSON wireEvent.
// The stream ends after a "done" or "error" event; the connection is
// kept open and flushed per event so the browser renders live.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		http.Error(w, "empty prompt", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// emit writes one wire event as an SSE frame and flushes so the
	// browser sees it immediately. It is called only from runStream's
	// goroutine (this handler blocks on it), so no extra locking is
	// needed around the ResponseWriter.
	emit := func(ev wireEvent) {
		fmt.Fprintf(w, "data: %s\n\n", ev.marshal())
		flusher.Flush()
	}

	// The request context cancels when the client disconnects; the
	// loop honours it and stops the run.
	if err := s.eng.runStream(r.Context(), req.Prompt, req.SessionID, emit); err != nil {
		// Surface run-setup failures (provider/loop build) as a final
		// error frame so the UI can show them instead of a dead stream.
		emit(wireEvent{Type: "error", Err: err.Error()})
		flusher.Flush()
	}
}
