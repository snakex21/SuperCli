package webgui

import (
	"context"
	"strings"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

// Legacy background reports use the user role for model delivery, not because
// a human authored them. Reclassify only a complete host-shaped envelope at
// the start of a plain message; quoted XML and questions containing a report
// must remain normal user messages. Stored messages themselves are unchanged.
func isTranscriptWorkerNotification(msg llm.Message) bool {
	if msg.Role != llm.RoleUser {
		return false
	}
	if msg.Name == llm.WorkerNotificationName {
		return true
	}
	if msg.Origin != nil && msg.Origin.Source == llm.SourceHuman {
		return false
	}
	if msg.Name != "" || msg.ToolCallID != "" || len(msg.ToolCalls) != 0 || len(msg.Parts) != 0 {
		return false
	}
	text := strings.TrimSpace(msg.Content)
	rest, ok := strings.CutPrefix(text, "<task-notification>")
	if !ok {
		return false
	}
	field := func(name string) (string, bool) {
		var matched bool
		rest, matched = strings.CutPrefix(strings.TrimSpace(rest), "<"+name+">")
		if !matched {
			return "", false
		}
		value, after, closed := strings.Cut(rest, "</"+name+">")
		if !closed || strings.ContainsAny(value, "<>\r\n") {
			return "", false
		}
		rest = after
		return strings.TrimSpace(value), true
	}
	id, ok := field("task-id")
	if !ok || !strings.HasPrefix(id, "worker-") || len(id) == len("worker-") {
		return false
	}
	for _, char := range id[len("worker-"):] {
		if char < '0' || char > '9' {
			return false
		}
	}
	if agent, valid := field("agent"); !valid || agent == "" {
		return false
	}
	if strings.HasPrefix(strings.TrimSpace(rest), "<worker-model>") {
		if model, valid := field("worker-model"); !valid || model == "" {
			return false
		}
		if _, valid := field("reasoning-effort"); !valid {
			return false
		}
	}
	status, ok := field("status")
	if !ok {
		return false
	}
	switch status {
	case "created", "running", "done", "completed", "failed", "stopped":
	default:
		return false
	}
	end := strings.LastIndex(rest, "</task-notification>")
	if end < 0 {
		return false
	}
	suffix := strings.TrimSpace(rest[end+len("</task-notification>"):])
	return suffix == "" || strings.HasPrefix(suffix, "[Attached: worker tool observations (historical snapshots).]")
}

// Real attachment records are human provenance even when the user pastes a
// complete legacy report. Match the same exception as history presentation.
func isStoredWorkerNotification(ctx context.Context, store *session.Store, sessionID string, seq int, msg llm.Message) (bool, error) {
	if !isTranscriptWorkerNotification(msg) {
		return false, nil
	}
	attachments, err := store.ReadMessageAttachmentsRange(ctx, sessionID, seq, seq)
	if err != nil {
		return false, err
	}
	return len(attachments[seq]) == 0, nil
}
