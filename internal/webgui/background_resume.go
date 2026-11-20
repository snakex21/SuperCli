package webgui

import (
	"encoding/json"
	"net/http"
	"strings"
)

// handleBackgroundResume: POST {"session": id} ends a hold at the automatic
// wake-up cap ("Automatic wake-up paused — Resume" banner) without the user
// having to type a message. The held worker reports start an automatic turn.
func (s *Server) handleBackgroundResume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Session string `json:"session"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	sid := strings.TrimSpace(req.Session)
	if sid == "" {
		http.Error(w, "session is required", http.StatusBadRequest)
		return
	}
	if !s.eng.ResumeBackgroundWake(sid) {
		// Nothing is held in this process (e.g. after a restart): clear a
		// stale banner; held reports are in history for the next message.
		s.eng.syncHub.backgroundWakeHeld(sid, 0, 0)
	}
	writeJSON(w, map[string]any{"ok": true})
}
