package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"

	"supercli/internal/agent"
)

var savedToolOutputHandle = regexp.MustCompile(`^(out_[0-9a-f]{6,32}|worker_ui_[0-9a-f]{32})$`)

func (e *Engine) savedToolOutput(ctx context.Context, sessionID, handle string) (string, error) {
	if !savedToolOutputHandle.MatchString(handle) {
		return "", fmt.Errorf("invalid output reference")
	}
	store, err := e.sessionStore()
	if err != nil {
		return "", err
	}
	meta, err := store.Get(sessionID)
	if err != nil {
		return "", err
	}
	if !sameSessionWorkspace(meta.Cwd, e.Home()) {
		return "", errSessionOutsideWorkspace
	}
	return store.ReadSessionToolOutput(ctx, sessionID, handle, agent.WorkerToolDetailLimit)
}

// Full payloads are fetched only when a human opens the corresponding row.
// The selected session and workspace own the immutable, bounded cache entry.
func (s *Server) handleSavedToolOutput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	handle := r.URL.Query().Get("handle")
	text, err := s.eng.savedToolOutput(r.Context(), r.URL.Query().Get("session"), handle)
	if err != nil {
		http.Error(w, "saved tool output is unavailable", http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if len(handle) > len("worker_ui_") && handle[:len("worker_ui_")] == "worker_ui_" {
		var detail agent.WorkerToolDetail
		if json.Unmarshal([]byte(text), &detail) != nil {
			http.Error(w, "invalid tool detail", http.StatusNotFound)
			return
		}
		writeJSON(w, detail)
		return
	}
	writeJSON(w, struct {
		Text string `json:"text"`
	}{text})
}
