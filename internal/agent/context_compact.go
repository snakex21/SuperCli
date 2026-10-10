package agent

import (
	"context"

	"supercli/internal/llm"
)

// Provider returns the loop's current provider. The /compact
// slash command uses it so summarization always runs on the
// active model, including after a /model swap.
func (l *Loop) Provider() llm.Provider {
	return l.provider
}

// LoadConversation replaces the conversation body with msgs,
// keeping the leading system messages (base prompt, pattern
// injection) intact. Used by /resume to load a prior session
// into the live loop. The loaded messages are NOT re-persisted
// (they already live in the session store under their original
// session id). Hidden flags are reset.
func (l *Loop) LoadConversation(msgs []llm.Message) {
	// Reload authorization from the resumed writer's raw transcript on Run,
	// never from this provider projection or its model-produced summary.
	l.downloadHumanContext = nil
	l.downloadHistoryLoaded = false
	l.conversationEpoch.Add(1)
	l.failedChecks.reset()
	l.resetModelContextBaseline()
	keep := 0
	for keep < len(l.Messages) && l.Messages[keep].Role == llm.RoleSystem {
		keep++
	}
	cleaned := l.cleanModelHistory(msgs)
	// A smaller replacement must release obsolete message slots and their payloads.
	// Keep external archive views intact instead of clearing the old array.
	messages := make([]llm.Message, keep+len(cleaned))
	if len(messages) == 0 && l.Messages == nil {
		messages = nil
	}
	copy(messages, l.Messages[:keep])
	copy(messages[keep:], cleaned)
	l.Messages = messages
	// The loaded body may come from a different session than this loop's
	// writer (/resume); its model identity is unknown until the next call.
	l.contextModel = contextModelState{loaded: true}
	l.resetHidden()
	l.chatWindowStart = 0
}

// CompactWithSummary replaces every non-system message with a
// single user message containing summary. Leading system
// messages (the base prompt, the F5.d pattern injection) are
// kept so the model's standing instructions survive compaction.
// The summary is saved only in the model context projection; the dropped
// messages remain in the F13 session store and stay searchable
// via search_history. Hidden flags for retained messages are remapped;
// flags for replaced messages disappear.
//
// Returns the number of messages removed.
func (l *Loop) CompactWithSummary(summary string) int {
	return l.CompactPrefixWithSummary(summary, len(l.Messages))
}

// CompactPrefixWithSummary is CompactWithSummary cutting at a turn
// boundary: only the non-system messages BEFORE upto are replaced by
// the summary; the tail [upto:) — typically the last user turn —
// survives verbatim, so the model never resumes from a summary of
// its own half-finished turn. Their hidden flags survive as well. upto is
// clamped to the message range; an empty replacement is a no-op.
//
// Returns the number of messages removed.
func (l *Loop) CompactPrefixWithSummary(summary string, upto int) int {
	keep := 0
	for keep < len(l.Messages) && l.Messages[keep].Role == llm.RoleSystem {
		keep++
	}
	if upto > len(l.Messages) {
		upto = len(l.Messages)
	}
	if upto < keep {
		upto = keep
	}
	removed := upto - keep
	if removed == 0 {
		return 0
	}
	oldHidden := l.hidden
	tail := l.Messages[upto:]
	// The summary rides as a USER message, not system: several chat
	// templates (e.g. Qwen3.5's Jinja) hard-reject any system message
	// that is not at the very beginning of the conversation, so a
	// mid-history system summary 400s the whole session. The resume
	// framing text (wrapCompactSummary) already reads naturally as a
	// user hand-off.
	sum := llm.Message{Role: llm.RoleUser, Content: summary}
	// Copy only retained messages into a fresh exact-size array. Reusing the
	// old capacity would keep the discarded prefix payloads alive beyond len.
	messages := make([]llm.Message, keep+1+len(tail))
	copy(messages, l.Messages[:keep])
	messages[keep] = sum
	copy(messages[keep+1:], tail)
	l.Messages = messages
	l.resetHidden()
	// Only the replaced prefix disappears. Preserve visibility for surviving
	// system messages and the untouched tail after their indices shift.
	keepHidden := func(oldIndex, newIndex int) {
		if oldIndex < len(oldHidden) && oldHidden[oldIndex] {
			l.ensureHidden(len(l.Messages))
			l.hidden[newIndex] = true
		}
	}
	for i := 0; i < keep; i++ {
		keepHidden(i, i)
	}
	for i := range tail {
		keepHidden(upto+i, keep+1+i)
	}
	l.chatWindowStart = 0
	l.persistProjection(context.Background())
	return removed
}
