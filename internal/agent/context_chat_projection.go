package agent

import (
	"strings"

	"supercli/internal/llm"
)

// chatHistoryProjection is shared by request assembly, estimation and pruning.
// It is pure: only request assembly commits nextStart. Index tracking is used
// for pruning; normal requests avoid that extra allocation.
func chatHistoryProjection(messages []llm.Message, start int, trackIndices bool) (view []llm.Message, indices []int, nextStart int) {
	lastUser := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == llm.RoleUser && !strings.Contains(messages[i].Content, "<task-notification>") {
			lastUser = i
			break
		}
	}
	end := len(messages)
	if lastUser >= 0 {
		end = lastUser
	}
	if start < 0 || start > end {
		start = 0
	}
	nextStart = start
	count, tokens := 0, 0
	for i := start; i < end; i++ {
		if chatWindowEligible(messages[i]) {
			count++
			tokens += llm.EstimateMessageTokens(messages[i])
		}
	}
	if tokens > chatWindowMaxTokens {
		// Jump once by the same number of eligible messages as the existing
		// growing window, then keep subsequent turns append-only again.
		kept, boundary := 0, end
		for i := end - 1; i >= start && kept < chatWindowKeepMsgs; i-- {
			if chatWindowEligible(messages[i]) {
				kept++
				boundary = i
			}
		}
		start, nextStart, count = boundary, boundary, kept
	}
	capacity := count
	if lastUser >= 0 {
		capacity += len(messages) - lastUser
	}
	view = make([]llm.Message, 0, capacity)
	if trackIndices {
		indices = make([]int, 0, capacity)
	}
	appendAt := func(i int) {
		view = append(view, messages[i])
		if trackIndices {
			indices = append(indices, i)
		}
	}
	for i := start; i < end; i++ {
		if chatWindowEligible(messages[i]) {
			appendAt(i)
		}
	}
	if lastUser >= 0 {
		for i := lastUser; i < len(messages); i++ {
			if messages[i].Role != llm.RoleSystem {
				appendAt(i)
			}
		}
	}
	return view, indices, nextStart
}

// Light routes also omit old tool exchanges and dialogue before their sticky
// chat window. Include visibility placeholders here so that window indices and
// user boundaries match the actual request exactly. Placeholder indices map to
// the first hidden entry; they can never be pruning victims (they are user text).
func (l *Loop) pruningHistory() compactionHistory {
	if l.route == RouteCoordinator {
		return l.compactionHistory()
	}
	h := compactionHistory{messages: l.VisibleMessages(), originalLength: len(l.Messages)}
	if l.hidden != nil {
		wasHidden := false
		for i := range l.Messages {
			hidden := i < len(l.hidden) && l.hidden[i]
			if !hidden || !wasHidden {
				h.originalIndices = append(h.originalIndices, i)
			}
			wasHidden = hidden
		}
	}
	h.messages = llm.ProjectReasoningHistory(l.provider, l.reasoningHistoryView(h.messages))
	resolved, resolvedIndices := l.resolvedToolProviderProjection(h.messages, true)
	h.requestMessages, h.requestIndices, _ = chatHistoryProjection(resolved, l.chatWindowStart, true)
	if resolvedIndices != nil {
		for i, index := range h.requestIndices {
			h.requestIndices[i] = resolvedIndices[index]
		}
	}
	return h
}
