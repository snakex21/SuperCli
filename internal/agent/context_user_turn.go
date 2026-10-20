package agent

import "supercli/internal/llm"

// isConversationUserTurn identifies a real instruction boundary. Internal
// task notifications and user-role image wrappers produced by tools must not
// count as recent user turns; doing so can make compaction preserve a giant
// active tool exchange and discard the actual user request instead.
func isConversationUserTurn(message llm.Message) bool {
	return llm.IsConversationUserTurn(message)
}
