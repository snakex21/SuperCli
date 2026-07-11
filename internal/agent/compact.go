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
	keep := 0
	for keep < len(l.Messages) && l.Messages[keep].Role == llm.RoleSystem {
		keep++
	}
	// Strip reasoning from resumed assistant turns so the live history
	// stays consistent with fresh turns (prior chain-of-thought is not
	// context). The session store still holds the full text.
	cleaned := make([]llm.Message, len(msgs))
	for i, m := range msgs {
		if m.Role == llm.RoleAssistant {
			cleaned[i] = stripThinkingFromMessage(m)
		} else {
			cleaned[i] = m
		}
	}
	l.Messages = append(l.Messages[:keep], cleaned...)
	l.resetHidden()
	l.chatWindowStart = 0
}

// CompactWithSummary replaces every non-system message with a
// single system message containing summary. Leading system
// messages (the base prompt, the F5.d pattern injection) are
// kept so the model's standing instructions survive compaction.
// The summary message is persisted like any other; the dropped
// messages remain in the F13 session store and stay searchable
// via search_history. Hidden flags are reset because the
// indices they referred to no longer exist.
//
// Returns the number of messages removed.
func (l *Loop) CompactWithSummary(summary string) int {
	return l.CompactPrefixWithSummary(summary, len(l.Messages))
}

// CompactPrefixWithSummary is CompactWithSummary cutting at a turn
// boundary: only the non-system messages BEFORE upto are replaced by
// the summary; the tail [upto:) — typically the last user turn —
// survives verbatim, so the model never resumes from a summary of
// its own half-finished turn. upto is clamped to the message range.
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
	tail := append([]llm.Message(nil), l.Messages[upto:]...)
	l.Messages = l.Messages[:keep]
	// The summary rides as a USER message, not system: several chat
	// templates (e.g. Qwen3.5's Jinja) hard-reject any system message
	// that is not at the very beginning of the conversation, so a
	// mid-history system summary 400s the whole session. The resume
	// framing text (wrapCompactSummary) already reads naturally as a
	// user hand-off.
	sum := llm.Message{Role: llm.RoleUser, Content: summary}
	l.Messages = append(l.Messages, sum)
	l.Messages = append(l.Messages, tail...)
	l.persist(context.Background(), sum)
	l.resetHidden()
	l.chatWindowStart = 0
	return removed
}
