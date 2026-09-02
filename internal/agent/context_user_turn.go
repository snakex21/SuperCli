package agent

import (
	"strings"

	"supercli/internal/llm"
)

// isConversationUserTurn identifies a real instruction boundary. Internal
// task notifications and user-role image wrappers produced by tools must not
// count as recent user turns; doing so can make compaction preserve a giant
// active tool exchange and discard the actual user request instead.
func isConversationUserTurn(message llm.Message) bool {
	if message.Role != llm.RoleUser || strings.Contains(message.Content, "<task-notification>") {
		return false
	}
	text := strings.TrimSpace(message.Content)
	if text == "" {
		for _, part := range message.Parts {
			if part.Type == llm.PartTypeText {
				text = strings.TrimSpace(part.Text)
				if text != "" {
					break
				}
			}
		}
	}
	return !strings.HasPrefix(text, "Attached image from tool ")
}
