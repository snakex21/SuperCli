package webgui

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"supercli/internal/llm"
	"supercli/internal/tools/core"
)

const workerObservationLimit = 128 * 1024

var workerObservationID = regexp.MustCompile(`^<task-notification>\s*<task-id>(worker-[0-9]+)</task-id>\s*<agent>[^<>\r\n]+</agent>`)
var workerObservationHeader = regexp.MustCompile(`^\[Worker (worker-[0-9]+) run [1-9][0-9]* tool observations at [0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]+)?Z; historical snapshots, not current workspace state; outputs may be truncated\](?:\r?\n|$)`)

type workerObservationReference struct {
	prefix, id, handle, inline string
}

// Legacy handoffs kept bounded evidence behind the host's stored-output
// reference, before the independent GUI journal existed. Never turn that text
// into invented per-call statuses; display the original observations verbatim.
func legacyWorkerObservations(msg llm.Message) (workerObservationReference, bool) {
	if len(msg.Parts) != 0 || len(msg.ToolCalls) != 0 || len(transcriptWorkerActivity(msg)) != 0 ||
		msg.Origin != nil && msg.Origin.Source == llm.SourceHuman {
		return workerObservationReference{}, false
	}
	if !(msg.Role == llm.RoleTool && (msg.Name == "task" || msg.Name == "send_message") && msg.ToolCallID != "" ||
		msg.Role == llm.RoleUser && isTranscriptWorkerNotification(msg)) {
		return workerObservationReference{}, false
	}
	src := strings.TrimSpace(msg.Content)
	fields := workerObservationID.FindStringSubmatch(src)
	end := strings.LastIndex(src, "</task-notification>")
	if len(fields) != 2 || end < 0 {
		return workerObservationReference{}, false
	}
	end += len("</task-notification>")
	ref := workerObservationReference{prefix: src[:end], id: fields[1]}
	suffix := strings.TrimSpace(src[end:])
	if header := workerObservationHeader.FindStringSubmatch(suffix); len(header) == 2 && header[1] == ref.id {
		ref.inline = suffix
		return ref, true
	}
	if strings.HasPrefix(suffix, "[Attached: worker tool observations (historical snapshots).]") {
		ref.handle = core.StoredOutputHandle(src)
		return ref, ref.handle != ""
	}
	return workerObservationReference{}, false
}

func (e *Engine) workerObservations(ctx context.Context, sessionID string, seq int, messageID string) (string, error) {
	if seq < 1 || messageID == "" {
		return "", fmt.Errorf("invalid observation reference")
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
	row, err := store.ReadTranscriptMessageAt(ctx, sessionID, seq)
	if err != nil || strconv.FormatInt(row.MessageID, 10) != messageID {
		return "", fmt.Errorf("observation message is unavailable")
	}
	msg, err := row.ToMessage()
	if err != nil {
		return "", err
	}
	ref, ok := legacyWorkerObservations(msg)
	if !ok {
		return "", fmt.Errorf("message has no saved worker observations")
	}
	attachments, err := store.ReadMessageAttachmentsRange(ctx, sessionID, seq, seq)
	if err != nil || len(attachments[seq]) != 0 {
		return "", fmt.Errorf("message is not a worker observation receipt")
	}
	text := ref.inline
	if ref.handle != "" {
		retained, err := store.ReadSessionToolOutput(ctx, sessionID, ref.handle, workerObservationLimit+len(ref.prefix))
		if err != nil {
			return "", err
		}
		suffix, matches := strings.CutPrefix(retained, ref.prefix)
		if !matches {
			return "", fmt.Errorf("saved output does not match the worker report")
		}
		text = strings.TrimSpace(suffix)
	}
	header := workerObservationHeader.FindStringSubmatch(text)
	if len(header) != 2 || header[1] != ref.id || len(text) > workerObservationLimit {
		return "", fmt.Errorf("saved observations do not match the worker receipt")
	}
	return text, nil
}

func (s *Server) handleWorkerObservations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	seq, err := strconv.Atoi(r.URL.Query().Get("seq"))
	if err != nil {
		http.Error(w, "invalid observation sequence", http.StatusBadRequest)
		return
	}
	text, err := s.eng.workerObservations(r.Context(), r.URL.Query().Get("session"), seq, r.URL.Query().Get("message_id"))
	if err != nil {
		http.Error(w, "saved worker observations are unavailable", http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, struct {
		Text string `json:"text"`
	}{Text: text})
}
