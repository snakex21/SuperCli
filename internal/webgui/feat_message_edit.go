package webgui

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

// handleSessionMessageEdit edits or restores one exact assistant reply.
// Same guards as rewind: no active work, workspace ownership and the
// physical message ID must still match the selected sequence.
func (s *Server) handleSessionMessageEdit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var b struct {
		SessionID string `json:"session_id"`
		Seq       int    `json:"seq"`
		MessageID string `json:"message_id"`
		Content   string `json:"content"`
		Restore   bool   `json:"restore"`
	}
	// Replies are text, never media; cap the payload.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&b); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	b.SessionID = strings.TrimSpace(b.SessionID)
	messageID, err := strconv.ParseInt(b.MessageID, 10, 64)
	if b.SessionID == "" || b.Seq <= 0 || err != nil || messageID <= 0 {
		http.Error(w, "session_id, positive seq and message_id are required", http.StatusBadRequest)
		return
	}
	if !b.Restore && strings.TrimSpace(b.Content) == "" {
		http.Error(w, "content is required", http.StatusBadRequest)
		return
	}
	cfg := s.eng.tomlConfig()
	if !config.AssistantEditEnabled(cfg) {
		http.Error(w, "editing AI replies is disabled (setting assistant_edit)", http.StatusForbidden)
		return
	}
	if !waitForActiveWorkToFinish(r.Context(), s.eng, 3*time.Second) {
		http.Error(w, "stop the active task before editing this conversation", http.StatusConflict)
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
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	receipt := session.MessageReceipt{Seq: b.Seq, ID: messageID}
	var result session.MessageEditResult
	if b.Restore {
		result, err = store.RestoreAssistantMessage(ctx, b.SessionID, receipt)
	} else {
		result, err = store.EditAssistantMessage(ctx, b.SessionID, receipt, b.Content, config.AssistantEditModelSeesEnabled(cfg))
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		http.Error(w, "selected message changed; reload the conversation", http.StatusConflict)
	case errors.Is(err, session.ErrNotAssistantMessage):
		http.Error(w, "seq must identify an AI reply", http.StatusBadRequest)
	case err != nil:
		writeWorkflowError(w, err)
	default:
		writeJSON(w, result)
	}
}
