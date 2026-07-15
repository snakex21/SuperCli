package webgui

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"supercli/internal/checkpoint"
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
		SessionID   string `json:"session_id"`
		ThroughSeq  int    `json:"through_seq"`
		SelectedSeq int    `json:"selected_seq,omitempty"`
		RewindFiles bool   `json:"rewind_files,omitempty"`
		Provider    string `json:"provider"`
		Model       string `json:"model"`
		Reasoning   string `json:"reasoning"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		http.Error(w, "bad request: "+err.Error(), 400)
		return
	}
	undone := checkpoint.BatchResult{}
	var err error
	if b.RewindFiles {
		if b.SelectedSeq <= 0 {
			http.Error(w, "selected_seq is required when rewinding files", http.StatusBadRequest)
			return
		}
		manager, managerErr := s.eng.checkpointManager(s.eng.Home())
		if managerErr != nil {
			http.Error(w, managerErr.Error(), http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		undone, err = manager.UndoFrom(ctx, strings.TrimSpace(b.SessionID), b.SelectedSeq)
		cancel()
		if err != nil {
			writeJSONStatus(w, http.StatusConflict, checkpointRewindView{
				Available:   len(undone.Records) > 0,
				Checkpoints: len(undone.Records),
				Files:       undone.Files,
				Conflicts:   undone.Conflicts,
			})
			return
		}
	}
	out, err := s.eng.forkSession(r.Context(), b.SessionID, b.ThroughSeq, b.Provider, b.Model, b.Reasoning)
	if err != nil {
		if len(undone.Records) > 0 {
			if manager, managerErr := s.eng.checkpointManager(s.eng.Home()); managerErr == nil {
				rollbackCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				rollbackErr := manager.RedoBatch(rollbackCtx, undone)
				cancel()
				if rollbackErr != nil {
					err = errors.Join(err, errors.New("file rewind rollback failed: "+rollbackErr.Error()))
				}
			}
		}
		writeWorkflowError(w, err)
		return
	}
	for _, record := range undone.Records {
		_ = s.appendCheckpointEvent(record, "undo", record.Files)
	}
	if len(undone.Records) > 0 {
		ids := make([]string, 0, len(undone.Records))
		for _, record := range undone.Records {
			ids = append(ids, record.ID)
		}
		out.FileRewind = &fileRewindReceipt{
			SessionID:     strings.TrimSpace(b.SessionID),
			CheckpointIDs: ids,
			Files:         undone.Files,
		}
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
