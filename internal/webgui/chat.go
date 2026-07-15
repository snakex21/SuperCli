package webgui

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// chatRequest is the JSON body posted to /api/chat.
type chatRequest struct {
	Prompt              string `json:"prompt"`
	SessionID           string `json:"session_id,omitempty"`
	Rewound             bool   `json:"rewound,omitempty"`
	RewindReason        string `json:"rewind_reason,omitempty"`
	RewindFiles         bool   `json:"rewind_files,omitempty"`
	RewindFilesRestored bool   `json:"rewind_files_restored,omitempty"`
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
	req.RewindReason = strings.TrimSpace(truncateRunes(req.RewindReason, 400))
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
	started := time.Now()
	eventCount := 0
	lastType := ""
	terminalSeen := false

	// emit writes one wire event as an SSE frame and flushes so the
	// browser sees it immediately. It is called only from runStream's
	// goroutine (this handler blocks on it), so no extra locking is
	// needed around the ResponseWriter.
	emit := func(ev wireEvent) {
		eventCount++
		lastType = ev.Type
		if ev.Type == "done" || ev.Type == "error" {
			terminalSeen = true
		}
		fmt.Fprintf(w, "data: %s\n\n", ev.marshal())
		flusher.Flush()
	}

	// The request context cancels when the client disconnects; the
	// loop honours it and stops the run.
	rewindFeedback := ""
	if req.Rewound {
		rewindFeedback = "[rewind_feedback]\nThe user deliberately rejected and rewound the previous attempt. Reconsider the approach and do not repeat it without verification."
		if req.RewindFiles {
			rewindFeedback += "\nThe program also restored the affected workspace files to their checkpointed state."
		} else if req.RewindFilesRestored {
			rewindFeedback += "\nThe user reconsidered and restored the complete file version produced by the rejected attempt. Preserve that workspace state unless the new request requires changing it."
		}
		if req.RewindReason != "" {
			rewindFeedback += "\nUser-provided reason: " + req.RewindReason
		}
		rewindFeedback += "\n[/rewind_feedback]"
	}
	if err := s.eng.runStream(r.Context(), req.Prompt, req.SessionID, rewindFeedback, emit); err != nil {
		// Surface run-setup failures (provider/loop build) as a final
		// error frame so the UI can show them instead of a dead stream.
		emit(wireEvent{Type: "error", Err: err.Error()})
		flusher.Flush()
		log.Printf("web chat ended with error: model=%q session=%q duration=%s events=%d err=%v", s.eng.ModelName(), req.SessionID, time.Since(started).Round(time.Millisecond), eventCount, err)
		return
	}
	log.Printf("web chat completed: model=%q session=%q duration=%s events=%d terminal=%t last=%q", s.eng.ModelName(), req.SessionID, time.Since(started).Round(time.Millisecond), eventCount, terminalSeen, lastType)
	if terminalSeen && lastType == "done" {
		signalNativeRunCompleted()
	}
}
