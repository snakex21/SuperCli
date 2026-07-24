package webgui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"supercli/internal/checkpoint"
	"supercli/internal/llm"
	"supercli/internal/storage/memory"
	"supercli/internal/storage/session"
)

type checkpointView struct {
	Available bool               `json:"available"`
	Record    *checkpoint.Record `json:"record,omitempty"`
	Files     []string           `json:"files,omitempty"`
	Conflicts []string           `json:"conflicts,omitempty"`
}

type checkpointRewindView struct {
	Available   bool     `json:"available"`
	Checkpoints int      `json:"checkpoints"`
	Files       []string `json:"files,omitempty"`
	Conflicts   []string `json:"conflicts,omitempty"`
}

func (s *Server) handleCheckpointRewind(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session"))
	fromSeq := queryInt(r, "from_seq", 0)
	if sessionID == "" || fromSeq <= 0 {
		http.Error(w, "session and positive from_seq are required", http.StatusBadRequest)
		return
	}
	if !s.checkpointSessionAllowed(sessionID) {
		http.Error(w, "session not found in active project", http.StatusNotFound)
		return
	}
	manager, err := s.eng.checkpointManager(s.eng.Home())
	if err != nil {
		if errors.Is(err, checkpoint.ErrUnavailable) {
			writeJSON(w, checkpointRewindView{})
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	preview := manager.PreviewFrom(sessionID, fromSeq)
	writeJSON(w, checkpointRewindView{
		Available:   len(preview.Records) > 0,
		Checkpoints: len(preview.Records),
		Files:       preview.Files,
	})
}

// handleCheckpointLesson records optional feedback after an undo. Session
// lessons guide only this branch; global lessons become durable preferences.
// No model call is made merely to store the feedback.
func (s *Server) handleCheckpointLesson(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		SessionID    string `json:"session_id"`
		CheckpointID string `json:"checkpoint_id"`
		Reason       string `json:"reason"`
		Scope        string `json:"scope"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request: "+err.Error(), 400)
		return
	}
	body.Reason = strings.TrimSpace(body.Reason)
	if body.Reason == "" {
		http.Error(w, "reason is required", 400)
		return
	}
	if len([]rune(body.Reason)) > 1000 {
		http.Error(w, "reason is too long", 400)
		return
	}
	if !s.checkpointSessionAllowed(strings.TrimSpace(body.SessionID)) {
		http.Error(w, "session not found in active project", 404)
		return
	}
	content := fmt.Sprintf("[undo feedback] The user reverted checkpoint %s because: %s. Avoid repeating that approach unless the user asks for it.", strings.TrimSpace(body.CheckpointID), body.Reason)
	if strings.EqualFold(strings.TrimSpace(body.Scope), "global") {
		store, err := memory.OpenStore(s.eng.DataDir())
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer store.Close()
		now := time.Now().UTC()
		err = store.Put(memory.Entry{ID: fmt.Sprintf("undo-%x", now.UnixNano()), Scope: memory.ScopePreference, Content: content, Tags: []string{"undo-feedback"}, Source: memory.SourceUser, CreatedAt: now})
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	} else {
		store, err := s.eng.sessionStore()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if err := session.NewWriter(store, body.SessionID).AppendMessage(r.Context(), llm.Message{Role: llm.RoleSystem, Content: content}); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleCheckpoint(w http.ResponseWriter, r *http.Request) {
	manager, err := s.eng.checkpointManager(s.eng.Home())
	if err != nil {
		if errors.Is(err, checkpoint.ErrUnavailable) {
			writeJSON(w, checkpointView{})
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if r.Method == http.MethodGet {
		sessionID := strings.TrimSpace(r.URL.Query().Get("session"))
		if sessionID == "" {
			http.Error(w, "missing session", http.StatusBadRequest)
			return
		}
		if !s.checkpointSessionAllowed(sessionID) {
			http.Error(w, "session not found in active project", http.StatusNotFound)
			return
		}
		record := manager.Latest(sessionID)
		writeJSON(w, checkpointView{Available: record != nil, Record: record})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		ID     string `json:"id"`
		Action string `json:"action"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var result checkpoint.Result
	switch strings.ToLower(strings.TrimSpace(body.Action)) {
	case "undo":
		result, err = manager.Undo(ctx, strings.TrimSpace(body.ID))
	case "redo":
		result, err = manager.Redo(ctx, strings.TrimSpace(body.ID))
	default:
		http.Error(w, "action must be undo or redo", http.StatusBadRequest)
		return
	}
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		}
		writeJSONStatus(w, status, checkpointView{Available: true, Record: &result.Record, Conflicts: result.Conflicts})
		return
	}
	_ = s.appendCheckpointEvent(result.Record, body.Action, result.Files)
	writeJSON(w, checkpointView{Available: true, Record: &result.Record, Files: result.Files})
}

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) checkpointSessionAllowed(id string) bool {
	store, err := s.eng.sessionStore()
	if err != nil {
		return false
	}
	meta, err := store.Get(id)
	return err == nil && sameSessionWorkspace(meta.Cwd, s.eng.Home())
}

func (s *Server) appendCheckpointEvent(record checkpoint.Record, action string, files []string) error {
	store, err := s.eng.sessionStore()
	if err != nil {
		return err
	}
	verb := "undid"
	if strings.EqualFold(action, "redo") {
		verb = "restored"
	}
	content := fmt.Sprintf("[checkpoint] User %s changes from turn %s (%d files). Current workspace state supersedes the earlier implementation.", verb, record.ID, len(files))
	return session.NewWriter(store, record.SessionID).AppendMessage(context.Background(), llm.Message{Role: llm.RoleSystem, Content: content})
}
