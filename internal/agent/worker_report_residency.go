package agent

import (
	"strings"

	"supercli/internal/llm"
)

// canonicalWorkerReport shares bytes already held by a completed worker's
// latest assistant reply. Caller owns the idle Loop (the worker run lock is
// still held). Exact equality is the only authorization to reuse storage;
// no history, native continuation state, report suffix or diagnostics change.
func canonicalWorkerReport(loop *Loop, report string) string {
	if loop == nil || report == "" {
		return report
	}
	for i := len(loop.Messages) - 1; i >= 0; i-- {
		message := loop.Messages[i]
		if message.Role != llm.RoleAssistant {
			continue
		}
		if len(message.ToolCalls) != 0 {
			return report
		}
		if len(message.Parts) == 0 {
			candidate := strings.TrimSpace(message.Content)
			if candidate == report {
				return candidate
			}
			return report
		}
		var candidate string
		textParts := 0
		for _, part := range message.Parts {
			switch part.Type {
			case llm.PartTypeText:
				textParts++
				candidate = part.Text
			case llm.PartTypeReasoning:
				// Native reasoning remains in history; it is not report text.
			default:
				return report
			}
		}
		if textParts == 1 {
			candidate = strings.TrimSpace(candidate)
			if candidate == report {
				return candidate
			}
		}
		return report
	}
	return report
}
