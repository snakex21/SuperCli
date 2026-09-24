package agent

import "supercli/internal/llm"

// reasoningHistoryView only changes the provider projection. The transcript,
// native payloads and UI archive remain available if the preference is reversed.
// Tool-call reasoning stays intact: some providers require it even in earlier
// turns. Active reasoning and unfinished tool exchanges are never removed.
func (l *Loop) reasoningHistoryView(messages []llm.Message) []llm.Message {
	if !l.discardPreviousReasoning {
		return messages
	}
	lastUser := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if isConversationUserTurn(messages[i]) {
			lastUser = i
			break
		}
	}
	completed := -1
	for i := 0; i < lastUser; i++ {
		if messages[i].Role == llm.RoleAssistant && len(messages[i].ToolCalls) == 0 && messageHasVisibleReply(messages[i]) {
			completed = i
		}
	}
	var out []llm.Message
	for i := 0; i <= completed; i++ {
		m := messages[i]
		if m.Role != llm.RoleAssistant || len(m.ToolCalls) > 0 || !m.HasNativeReasoning() {
			continue
		}
		if out == nil {
			out = append([]llm.Message(nil), messages...)
		}
		parts := make([]llm.ContentPart, 0, len(m.Parts))
		for _, p := range m.Parts {
			if p.Type != llm.PartTypeReasoning {
				parts = append(parts, p)
			}
		}
		m.Parts = parts
		if m.Content == "" && len(parts) == 0 {
			m.Content = noVisibleAnswerPlaceholder
		}
		out[i] = m
	}
	if out != nil {
		return out
	}
	return messages
}
