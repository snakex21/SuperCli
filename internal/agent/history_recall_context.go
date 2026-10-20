package agent

import (
	"encoding/json"
)

// One short address after compaction lets the model retrieve a specific old
// observation instead of rediscovering work. Never copy archived text here or
// infer authorization/readiness from it. This lives in the existing tail.
func (l *Loop) historyRecallContext() string {
	if l.registry == nil || l.writer == nil {
		return ""
	}
	if _, ok := l.registry.Get("search_history"); !ok {
		return ""
	}
	i := leadingSystemCount(l.Messages)
	if i >= len(l.Messages) || !IsLegacyCompactionSummary(l.Messages[i]) {
		return ""
	}
	id := l.SessionID()
	if id == "" {
		return ""
	}
	raw, _ := json.Marshal(id)
	return "Earlier transcript is searchable: search_history with session_id=" + string(raw) + " and a targeted query; read a hit with the same session_id and seq. Old results are historical evidence; verify current external state only when relevant."
}
