package agent

import (
	"sort"

	"supercli/internal/llm"
)

// compactionHistory applies the same visibility and native-reasoning policies
// as the outgoing request, while retaining tool evidence for the summarizer.
// Context reduction uses this view; the archive and ordinary request path do
// not change. originalIndices maps visible positions to canonical history.
type compactionHistory struct {
	messages        []llm.Message
	originalIndices []int
	originalLength  int
	requestMessages []llm.Message
	requestIndices  []int // request index -> index in messages; nil means unchanged
}

func (l *Loop) compactionHistory() compactionHistory {
	view := compactionHistory{messages: l.Messages, originalLength: len(l.Messages)}
	hasHidden := false
	for _, hidden := range l.hidden[:min(len(l.hidden), len(l.Messages))] {
		if hidden {
			hasHidden = true
			break
		}
	}
	if hasHidden {
		view.messages = nil
		for i, message := range l.Messages {
			if i < len(l.hidden) && l.hidden[i] {
				continue
			}
			view.originalIndices = append(view.originalIndices, i)
			view.messages = append(view.messages, message)
		}
	}
	// Project the FULL visible history before cutting it: the latest real user
	// turn determines whether earlier completed reasoning may be discarded.
	// These two projections preserve the number and order of messages.
	view.messages = llm.ProjectReasoningHistory(l.provider, l.reasoningHistoryView(view.messages))
	// Completed tool results may already be absent from requests. Keep them as
	// summary evidence, but never count their archived bytes as future savings.
	if l.route != RouteCoordinator {
		// Use the exact light-route window for cost and turn selection, but
		// retain the full visible evidence prefix for the summarizer. Its facts
		// must still support a later return to project work.
		request := l.pruningHistory()
		view.requestMessages = nil
		view.requestIndices = make([]int, 0, len(request.requestMessages))
		for i, message := range request.requestMessages {
			original := request.requestOriginalIndex(i)
			if original < len(l.hidden) && l.hidden[original] {
				continue // visibility placeholders are not real instruction turns
			}
			index := original
			if view.originalIndices != nil {
				index = sort.SearchInts(view.originalIndices, original)
			}
			view.requestMessages = append(view.requestMessages, message)
			view.requestIndices = append(view.requestIndices, index)
		}
	} else {
		view.requestMessages, view.requestIndices = l.resolvedToolProviderProjection(view.messages, true)
	}
	return view
}

func (h compactionHistory) originalSplit(split int) int {
	if split >= len(h.messages) {
		return h.originalLength
	}
	if h.originalIndices != nil {
		return h.originalIndices[split]
	}
	return split
}

// compactSplit selects a user-turn boundary by outgoing cost, then maps it back
// to the evidence view. Resolved-tool omission never drops user messages.
func (h compactionHistory) compactSplit(window int) int {
	return h.mapRequestSplit(compactSplit(h.requestMessages, window))
}

func (h compactionHistory) autoCompactSplit() int {
	return h.mapRequestSplit(autoCompactSplit(h.requestMessages))
}

func (h compactionHistory) mapRequestSplit(split int) int {
	if split <= leadingSystemCount(h.requestMessages) {
		return leadingSystemCount(h.messages)
	}
	if split >= len(h.requestMessages) {
		return len(h.messages)
	}
	if h.requestIndices != nil {
		return h.requestIndices[split]
	}
	return split
}

// requestPrefix is priced only after projecting the entire conversation: a
// later final reply can make an earlier call/result pair eligible for omission.
// Projecting an isolated prefix would give that pair a different retention rule.
func (h compactionHistory) requestPrefix(split int) []llm.Message {
	if h.requestIndices != nil {
		split = sort.SearchInts(h.requestIndices, split)
	}
	return h.requestMessages[:split]
}

func (h compactionHistory) requestOriginalIndex(index int) int {
	if h.requestIndices != nil {
		index = h.requestIndices[index]
	}
	return h.originalSplit(index)
}

// Only called for a proposed pruning batch that already passed the cheap gain
// gate. Originals and protocol fields remain untouched until this check accepts.
func (l *Loop) projectedPruneGain(h compactionHistory, victims []pruneVictim, handle string) int {
	candidate := append([]llm.Message(nil), h.messages...)
	for _, victim := range victims {
		index := victim.index
		if h.originalIndices != nil {
			index = sort.SearchInts(h.originalIndices, index)
		}
		candidate[index].Content = victim.archiveMarker(handle)
		candidate[index].Parts = nil
	}
	projected := l.resolvedToolProviderView(candidate)
	if l.route != RouteCoordinator {
		projected, _, _ = chatHistoryProjection(projected, l.chatWindowStart, false)
	}
	return llm.EstimateTokens(h.requestMessages) - llm.EstimateTokens(projected)
}
