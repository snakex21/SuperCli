package webgui

import (
	"encoding/json"

	"supercli/internal/llm"
)

// UI metadata comes only from a host-authored worker handoff, never from its
// report text or from human-supplied JSON. The browser also checks worker/run.
func transcriptWorkerActivity(msg llm.Message) json.RawMessage {
	if msg.Origin == nil || len(msg.Origin.WorkerActivity) == 0 || len(msg.Origin.WorkerActivity) > 512*1024 {
		return nil
	}
	owned := msg.Role == llm.RoleTool && (msg.Name == "task" || msg.Name == "send_message") && msg.Origin.Source == llm.SourceTaskResult ||
		msg.Role == llm.RoleUser && msg.Name == llm.WorkerNotificationName && msg.Origin.Source == llm.SourceWorkerNotification
	if !owned || !json.Valid(msg.Origin.WorkerActivity) {
		return nil
	}
	return msg.Origin.WorkerActivity
}
