package agent

import (
	"supercli/internal/llm"
)

// resolvedToolProviderView removes completed tool protocol envelopes from the
// provider-facing projection. The canonical in-memory history and persisted
// transcript stay untouched, so the UI can render every tool card and
// search_history can retrieve the original text on demand.
//
// An unresolved tail is always retained. Dropping a tool call before its
// result has been consumed would break provider protocol and prevent the next
// model step from completing the work.
func (l *Loop) resolvedToolProviderView(messages []llm.Message) []llm.Message {
	if l == nil || l.registry == nil {
		return messages
	}
	if _, ok := l.registry.Get("search_history"); !ok {
		return messages
	}
	return omitResolvedToolHistory(messages)
}

func omitResolvedToolHistory(messages []llm.Message) []llm.Message {
	if len(messages) == 0 {
		return messages
	}

	drop := make([]bool, len(messages))
	hasLaterFinal := false
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message.Role != llm.RoleAssistant {
			continue
		}
		if len(message.ToolCalls) == 0 {
			if messageHasVisibleReply(message) {
				hasLaterFinal = true
			}
			continue
		}
		if !hasLaterFinal {
			continue
		}

		drop[index] = true
		callIDs := make(map[string]struct{}, len(message.ToolCalls))
		for _, call := range message.ToolCalls {
			if call.ID != "" {
				callIDs[call.ID] = struct{}{}
			}
		}
		// Tool results for one assistant call batch precede the next assistant
		// message. Bound the scan to that block so providers that reuse call IDs
		// in later turns cannot cause an unrelated live result to disappear.
		for resultIndex := index + 1; resultIndex < len(messages); resultIndex++ {
			candidate := messages[resultIndex]
			if candidate.Role == llm.RoleAssistant {
				break
			}
			if candidate.Role != llm.RoleTool {
				continue
			}
			if _, ok := callIDs[candidate.ToolCallID]; ok {
				drop[resultIndex] = true
			}
		}
	}

	dropped := 0
	for _, marked := range drop {
		if marked {
			dropped++
		}
	}
	if dropped == 0 {
		return messages
	}
	out := make([]llm.Message, 0, len(messages)-dropped)
	for index, message := range messages {
		if !drop[index] {
			out = append(out, message)
		}
	}
	return out
}

func messageHasVisibleReply(message llm.Message) bool {
	if hasVisibleUserReply(message.Content) {
		return true
	}
	for _, part := range message.Parts {
		if part.Type != llm.PartTypeText || hasVisibleUserReply(part.Text) {
			return true
		}
	}
	return false
}
