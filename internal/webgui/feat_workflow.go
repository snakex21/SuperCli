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
	"supercli/internal/llm"
	"supercli/internal/storage/session"
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
			ID       string  `json:"id"`
			Prompt   *string `json:"prompt"`
			Position *int    `json:"position"`
		}
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			http.Error(w, "bad request: "+err.Error(), 400)
			return
		}
		b.ID = strings.TrimSpace(b.ID)
		if b.ID == "" || (b.Prompt == nil && b.Position == nil) {
			http.Error(w, "id and prompt or position are required", http.StatusBadRequest)
			return
		}
		if b.Prompt != nil {
			if err := s.eng.updateTask(r.Context(), b.ID, *b.Prompt); err != nil {
				writeWorkflowError(w, err)
				return
			}
		}
		if b.Position != nil {
			if err := s.eng.moveTask(r.Context(), b.ID, *b.Position); err != nil {
				writeWorkflowError(w, err)
				return
			}
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

type sessionRewindView struct {
	OK           bool     `json:"ok"`
	Removed      int      `json:"removed"`
	FilesRewound bool     `json:"files_rewound"`
	Files        []string `json:"files,omitempty"`
	Attachments  []string `json:"attachments,omitempty"`
	Warning      string   `json:"warning,omitempty"`
}

// handleSessionRewind rewinds one conversation in place. There is deliberately
// no implicit branch: the selected user message and everything after it are
// removed, while the caller keeps the selected text locally in the composer.
func (s *Server) handleSessionRewind(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var b struct {
		SessionID   string `json:"session_id"`
		SelectedSeq int    `json:"selected_seq"`
		RewindFiles bool   `json:"rewind_files"`
		Reason      string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		http.Error(w, "bad request: "+err.Error(), 400)
		return
	}
	b.SessionID = strings.TrimSpace(b.SessionID)
	b.Reason = strings.TrimSpace(truncateRunes(b.Reason, 400))
	if b.SessionID == "" || b.SelectedSeq <= 0 {
		http.Error(w, "session_id and positive selected_seq are required", http.StatusBadRequest)
		return
	}
	// Browser AbortController closes the SSE stream immediately, while the
	// canceled agent goroutine may need a brief moment to flush persistence and
	// release activeRuns. Let a rewind clicked directly after Stop wait for that
	// cleanup instead of failing a race the user cannot resolve.
	if !waitForActiveWorkToFinish(r.Context(), s.eng, 3*time.Second) {
		http.Error(w, "stop the active task before rewinding this conversation", http.StatusConflict)
		return
	}
	store, err := s.eng.sessionStore()
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	meta, err := store.Get(b.SessionID)
	if err != nil || !sameSessionWorkspace(meta.Cwd, s.eng.Home()) {
		writeWorkflowError(w, errSessionOutsideWorkspace)
		return
	}
	messages, err := store.ReadMessages(r.Context(), b.SessionID)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	selectedUser := false
	for _, message := range messages {
		if message.Seq == b.SelectedSeq && message.Role == string(llm.RoleUser) {
			selectedUser = true
			break
		}
	}
	if !selectedUser {
		http.Error(w, "selected_seq must identify a user message", http.StatusBadRequest)
		return
	}
	attachmentRows, err := store.ReadMessageAttachmentsRange(r.Context(), b.SessionID, b.SelectedSeq, b.SelectedSeq)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	selectedAttachments := append([]string(nil), attachmentRows[b.SelectedSeq]...)

	undone := checkpoint.BatchResult{}
	manager, managerErr := s.eng.checkpointManager(s.eng.Home())
	if b.RewindFiles {
		if managerErr != nil {
			http.Error(w, managerErr.Error(), http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		undone, err = manager.UndoFrom(ctx, b.SessionID, b.SelectedSeq)
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
	removed, err := store.TruncateFrom(r.Context(), b.SessionID, b.SelectedSeq)
	if err != nil {
		if len(undone.Records) > 0 {
			rollbackCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			rollbackErr := manager.RedoBatch(rollbackCtx, undone)
			cancel()
			if rollbackErr != nil {
				err = errors.Join(err, errors.New("file rewind rollback failed: "+rollbackErr.Error()))
			}
		}
		writeWorkflowError(w, err)
		return
	}

	out := sessionRewindView{
		OK:           true,
		Removed:      removed,
		FilesRewound: b.RewindFiles,
		Files:        undone.Files,
		Attachments:  selectedAttachments,
	}
	if managerErr == nil {
		if err := manager.ForgetFrom(b.SessionID, b.SelectedSeq); err != nil {
			out.Warning = "conversation was rewound, but old checkpoint metadata could not be detached: " + err.Error()
		}
	}
	marker := "[conversation rewind] The user permanently removed the selected message and the later conversation. Do not rely on the discarded attempt."
	if b.RewindFiles {
		marker += " The associated checkpointed file changes were also restored."
	} else {
		marker += " Workspace files were deliberately left unchanged and are the current source of truth; re-read relevant files before editing."
	}
	if b.Reason != "" {
		marker += " User reason: " + b.Reason
	}
	if appendErr := session.NewWriter(store, b.SessionID).AppendMessage(r.Context(), llm.Message{Role: llm.RoleSystem, Content: marker}); appendErr != nil {
		if out.Warning != "" {
			out.Warning += "; "
		}
		out.Warning += "rewind marker could not be saved: " + appendErr.Error()
	}
	writeJSON(w, out)
}

func waitForActiveWorkToFinish(ctx context.Context, eng *Engine, timeout time.Duration) bool {
	if eng == nil || !eng.HasActiveWork() {
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return !eng.HasActiveWork()
		case <-ticker.C:
			if !eng.HasActiveWork() {
				return true
			}
		}
	}
}

func writeWorkflowError(w http.ResponseWriter, err error) {
	if errors.Is(err, errSessionOutsideWorkspace) || errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "not found in active project", 404)
		return
	}
	http.Error(w, err.Error(), 400)
}
