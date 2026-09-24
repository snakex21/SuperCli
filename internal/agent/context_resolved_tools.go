package agent

import (
	"sort"

	"supercli/internal/llm"
)

// resolvedToolProviderView bounds completed tool evidence in the
// provider-facing projection. The canonical in-memory history and persisted
// transcript stay untouched, so the UI can render every tool card and
// search_history can retrieve the original text on demand.
//
// An unresolved tail is always retained. Dropping a tool call before its
// result has been consumed would break provider protocol and prevent the next
// model step from completing the work.
func (l *Loop) resolvedToolProviderView(messages []llm.Message) []llm.Message {
	if l == nil || l.registry == nil || l.writer == nil {
		return messages
	}
	if _, ok := l.registry.Get("search_history"); !ok {
		return messages
	}
	// A history tool may point at a parent/global store. Its presence does
	// not mean this loop's own tool results were saved there. In-memory
	// workers must retain their evidence across send_message continuations.
	// Failed or lost appends likewise make on-demand retrieval unreliable.
	h := &l.persistHealth
	h.mu.Lock()
	retrievable := !h.outage && len(h.pending) == 0 && h.dropped == 0
	h.mu.Unlock()
	if !retrievable {
		return messages
	}
	return omitResolvedToolHistory(messages)
}

func omitResolvedToolHistory(messages []llm.Message) []llm.Message {
	if len(messages) == 0 {
		return messages
	}

	// Native continuation state can depend on earlier tool evidence. Keep
	// that chain until ordinary budget-driven pruning/compaction, instead
	// of deleting it immediately after the final answer.
	for _, message := range messages {
		if message.HasNativeReasoning() {
			return messages
		}
	}

	drop := make([]bool, len(messages))
	var trimmedCalls map[int][]llm.ToolCall
	recentStart := recentCompletedTurnStart(messages)
	remaining := recentToolEvidenceBytes
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
		eligible := index >= recentStart && remaining > 0
		cost := 0
		var results []int
		if eligible {
			cost = toolEvidenceBytes(message)
			results = make([]int, 0, len(message.ToolCalls))
		}
		callIDs := make(map[string]bool, len(message.ToolCalls))
		for _, call := range message.ToolCalls {
			if call.ID != "" {
				callIDs[call.ID] = false
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
				if eligible {
					callIDs[candidate.ToolCallID] = true
					results = append(results, resultIndex)
					cost += toolEvidenceBytes(candidate)
				}
			}
		}
		if !eligible {
			continue
		}
		complete := len(callIDs) == len(message.ToolCalls) && len(results) == len(callIDs)
		for _, seen := range callIDs {
			complete = complete && seen
		}
		// Keep small exchanges from the latest completed user turn. They retain
		// their original chronology without a summary request or new instruction.
		// Large/older results remain retrievable from canonical history.
		if complete && cost <= remaining {
			remaining -= cost
			drop[index] = false
			for _, resultIndex := range results {
				drop[resultIndex] = false
			}
		} else if complete && len(message.ToolCalls) > 1 {
			calls, keptResults, keptCost := fitRecentToolBatch(message, messages, results, remaining)
			if len(calls) > 0 {
				if trimmedCalls == nil {
					trimmedCalls = make(map[int][]llm.ToolCall)
				}
				trimmedCalls[index] = calls
				remaining -= keptCost
				drop[index] = false
				for _, resultIndex := range keptResults {
					drop[resultIndex] = false
				}
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
			if calls, ok := trimmedCalls[index]; ok {
				message.ToolCalls = calls
			}
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
		if part.Type == llm.PartTypeReasoning {
			continue
		}
		if part.Type != llm.PartTypeText || hasVisibleUserReply(part.Text) {
			return true
		}
	}
	return false
}

// Includes arguments, result text and an envelope allowance, not just the
// displayed content. This is a payload-byte bound, not a tokenizer estimate.
const recentToolEvidenceBytes = 4 * 1024

func recentCompletedTurnStart(messages []llm.Message) int {
	final := -1
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Role == llm.RoleAssistant && len(m.ToolCalls) == 0 && messageHasVisibleReply(m) {
			final = i
			break
		}
	}
	if final < 0 {
		return len(messages)
	}
	for i := final - 1; i >= 0; i-- {
		if isConversationUserTurn(messages[i]) {
			return i
		}
	}
	return 0
}

func toolEvidenceBytes(m llm.Message) int {
	n := len(m.Content) + len(m.Name) + len(m.ToolCallID) + 64
	for _, p := range m.Parts {
		// Never reattach images, native state or hidden reasoning as a side effect.
		if p.Type != llm.PartTypeText {
			return recentToolEvidenceBytes + 1
		}
		n += len(p.Text)
	}
	for _, c := range m.ToolCalls {
		n += toolCallEvidenceBytes(c)
	}
	return n
}

// fitRecentToolBatch keeps complete call/result pairs when a single large
// result would otherwise discard a whole recent batch. This only runs after
// IDs/results have been validated and native continuation state excluded.
func fitRecentToolBatch(message llm.Message, history []llm.Message, results []int, budget int) ([]llm.ToolCall, []int, int) {
	base := message
	base.ToolCalls = nil
	cost := toolEvidenceBytes(base)
	if cost >= budget {
		return nil, nil, 0
	}
	byID := make(map[string]int, len(results))
	for _, index := range results {
		byID[history[index].ToolCallID] = index
	}
	type exchange struct {
		call, result, cost int
	}
	exchanges := make([]exchange, 0, len(message.ToolCalls))
	for i, call := range message.ToolCalls {
		index := byID[call.ID]
		exchanges = append(exchanges, exchange{i, index, toolCallEvidenceBytes(call) + toolEvidenceBytes(history[index])})
	}
	// Small observations release room for others. Retention priority changes,
	// but the outgoing messages and calls keep their original chronology.
	sort.SliceStable(exchanges, func(i, j int) bool { return exchanges[i].cost < exchanges[j].cost })
	keep := make([]bool, len(message.ToolCalls))
	var keptResults []int
	for _, pair := range exchanges {
		if pair.cost > budget-cost {
			continue
		}
		cost += pair.cost
		keep[pair.call] = true
		keptResults = append(keptResults, pair.result)
	}
	if len(keptResults) == 0 {
		return nil, nil, 0
	}
	calls := make([]llm.ToolCall, 0, len(keptResults))
	for i, call := range message.ToolCalls {
		if keep[i] {
			calls = append(calls, call)
		}
	}
	return calls, keptResults, cost
}

func toolCallEvidenceBytes(call llm.ToolCall) int {
	return len(call.Name) + len(call.ID) + len(call.Arguments) + 64
}
