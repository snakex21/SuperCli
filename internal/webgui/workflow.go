package webgui

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		out, err := s.eng.queuedTasks(r.Context())
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, out)
	case http.MethodPost:
		var b struct {
			SessionID string `json:"session_id"`
			Prompt    string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			http.Error(w, "bad request: "+err.Error(), 400)
			return
		}
		out, err := s.eng.enqueueTask(r.Context(), strings.TrimSpace(b.SessionID), b.Prompt)
		if err != nil {
			writeWorkflowError(w, err)
			return
		}
		writeJSON(w, out)
	case http.MethodPatch:
		var b struct {
			ID       string `json:"id"`
			Position int    `json:"position"`
		}
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			http.Error(w, "bad request: "+err.Error(), 400)
			return
		}
		if err := s.eng.moveTask(r.Context(), b.ID, b.Position); err != nil {
			writeWorkflowError(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	case http.MethodDelete:
		if err := s.eng.deleteTask(r.Context(), r.URL.Query().Get("id")); err != nil {
			writeWorkflowError(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (s *Server) handleBranches(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		out, err := s.eng.branchSessions(r.Context(), strings.TrimSpace(r.URL.Query().Get("session")))
		if err != nil {
			writeWorkflowError(w, err)
			return
		}
		writeJSON(w, out)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var b struct {
		SessionID  string `json:"session_id"`
		ThroughSeq int    `json:"through_seq"`
		Provider   string `json:"provider"`
		Model      string `json:"model"`
		Reasoning  string `json:"reasoning"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		http.Error(w, "bad request: "+err.Error(), 400)
		return
	}
	out, err := s.eng.forkSession(r.Context(), b.SessionID, b.ThroughSeq, b.Provider, b.Model, b.Reasoning)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	writeJSON(w, out)
}

func writeWorkflowError(w http.ResponseWriter, err error) {
	if errors.Is(err, errSessionOutsideWorkspace) || errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "not found in active project", 404)
		return
	}
	http.Error(w, err.Error(), 400)
}
