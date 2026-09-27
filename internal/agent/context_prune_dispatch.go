package agent

import (
	"encoding/json"
	"strings"

	"supercli/internal/llm"
)

// pruneSourceMessage returns a copy with the requested target name only for
// interpreting owned result framing. The persisted/provider protocol pair is
// unchanged. Late discovery in the same batch can leave it named invoke_tool.
func (l *Loop) pruneSourceMessage(i int) llm.Message {
	result := l.Messages[i]
	if result.Name != invokeToolName || result.ToolCallID == "" {
		return result
	}
	// A result belongs to the immediately preceding assistant batch. Never
	// borrow a reused call ID from an earlier assistant or user turn.
	for j := i - 1; j >= 0; j-- {
		message := l.Messages[j]
		if isConversationUserTurn(message) {
			return result
		}
		if message.Role != llm.RoleAssistant {
			continue
		}
		var call *llm.ToolCall
		for k := range message.ToolCalls {
			if message.ToolCalls[k].ID != result.ToolCallID {
				continue
			}
			if call != nil {
				return result // ambiguous duplicate ID
			}
			call = &message.ToolCalls[k]
		}
		if call == nil || call.Name != invokeToolName {
			return result
		}
		var envelope map[string]json.RawMessage
		if json.Unmarshal([]byte(call.Arguments), &envelope) != nil {
			return result
		}
		var target string
		if json.Unmarshal(envelope["tool"], &target) != nil {
			return result
		}
		// These are the only names with special pruning semantics. Do not turn
		// arbitrary extension names or file contents into executable metadata.
		switch target = strings.TrimSpace(target); target {
		case "ctx_execute", "process_session", "task", "send_message", "apply_skill", "read_many":
			result.Name = target
		}
		return result
	}
	return result
}
