package agent

import (
	"context"
	"fmt"

	"supercli/internal/llm"
)

// HideRange marks l.Messages[from:to) as hidden from the
// provider's context view. The messages stay in l.Messages
// (and remain persisted to the F13 session store +
// searchable via F13 search_history), but
// VisibleMessages() replaces each consecutive run of hidden
// entries with a single placeholder system message:
//
//	"[earlier context cleared — N message(s) compacted]"
//
// from and to are 0-based half-open slice indices. The
// function returns an error if the range is out of bounds.
// from == to is a no-op.
func (l *Loop) HideRange(from, to int) error {
	if from < 0 || to > len(l.Messages) || from > to {
		return fmt.Errorf("agent.HideRange: invalid range [%d, %d) for len=%d",
			from, to, len(l.Messages))
	}
	if from == to {
		return nil
	}
	l.ensureHidden(len(l.Messages))
	for i := from; i < to; i++ {
		l.hidden[i] = true
	}
	return nil
}

// HideLastUserTurns hides all but the last `keep` user
// messages plus their directly-paired assistant / tool
// messages. Used by the /clear slash command to drop
// everything except the most recent turn pair.
func (l *Loop) HideLastUserTurns(keep int) (hidden int) {
	if keep < 0 {
		keep = 0
	}
	// Find indices of user messages in reverse order.
	userIdx := make([]int, 0, len(l.Messages))
	for i, m := range l.Messages {
		if m.Role == llm.RoleUser {
			userIdx = append(userIdx, i)
		}
	}
	if len(userIdx) <= keep {
		return 0 // nothing to hide
	}
	cutoff := userIdx[len(userIdx)-keep] // hide everything before
	l.ensureHidden(len(l.Messages))
	for i := 0; i < cutoff; i++ {
		if !l.hidden[i] {
			l.hidden[i] = true
			hidden++
		}
	}
	return hidden
}

// VisibleMessages returns l.Messages with consecutive runs
// of hidden entries collapsed into a single placeholder.
// The placeholder is a system message; the model sees it
// as part of the system context but cannot tell where in
// the original conversation the cleared range was.
func (l *Loop) VisibleMessages() []llm.Message {
	out := make([]llm.Message, 0, len(l.Messages))
	runStart := -1
	flush := func(end int) {
		if runStart < 0 {
			return
		}
		n := end - runStart
		out = append(out, llm.Message{
			Role: llm.RoleSystem,
			Content: fmt.Sprintf(
				"[earlier context cleared — %d message(s) compacted]",
				n,
			),
		})
		runStart = -1
	}
	for i, m := range l.Messages {
		hidden := i < len(l.hidden) && l.hidden[i]
		if hidden {
			if runStart < 0 {
				runStart = i
			}
			continue
		}
		flush(i)
		out = append(out, m)
	}
	flush(len(l.Messages))
	return out
}

// EstimateVisibleTokens approximates the token count of
// VisibleMessages() using a chars/4 heuristic. Good enough
// for budget-based eviction; not for billing. The
// heuristic is intentionally cheap — we call it after
// every step in the loop, so anything O(n) on the message
// length is fine.
func (l *Loop) EstimateVisibleTokens() int {
	n := 0
	for _, m := range l.VisibleMessages() {
		n += len(m.Content) / 4
		for _, p := range m.Parts {
			if p.Type == llm.PartTypeText {
				n += len(p.Text) / 4
			}
		}
	}
	return n
}

// EvictForBudget hides the oldest non-system messages
// until the visible token estimate drops at or below 80%
// of the F7 per-session cap. Returns the number of
// messages evicted. Emits a MessagesHiddenEvent when
// anything was evicted.
//
// When CreditTracker is nil or the per-session cap is 0
// (unlimited), this is a no-op.
func (l *Loop) EvictForBudget(ctx context.Context, out chan<- Event) (evicted int) {
	if l.creditTracker == nil {
		return 0
	}
	sessionUsed, _ := l.creditTracker.Used()
	if sessionUsed <= 0 {
		return 0
	}
	threshold := int64(float64(sessionUsed) * 0.8)
	if threshold <= 0 {
		return 0
	}
	for l.EstimateVisibleTokens() > int(threshold) {
		idx := l.findOldestEvictable()
		if idx < 0 {
			break
		}
		l.ensureHidden(len(l.Messages))
		if l.hidden[idx] {
			break // safety: shouldn't happen
		}
		l.hidden[idx] = true
		evicted++
	}
	if evicted > 0 && out != nil {
		select {
		case out <- MessagesHiddenEvent{Count: evicted, Reason: "budget"}:
		case <-ctx.Done():
		}
	}
	return evicted
}

// HiddenCount returns the number of currently-hidden
// messages. Useful for tests and TUI status.
func (l *Loop) HiddenCount() int {
	n := 0
	for _, h := range l.hidden {
		if h {
			n++
		}
	}
	return n
}

// AllMessages returns the full, un-trimmed message list
// (including hidden entries). The F14 hide_messages
// tool needs the raw length for its range validation.
// VisibleMessages() is what the provider sees.
func (l *Loop) AllMessages() []llm.Message {
	return l.Messages
}

// resetHidden clears the hidden map. Called at the start
// of every Run so a previous Run's hides don't leak.
func (l *Loop) resetHidden() {
	l.hidden = nil
}

// ensureHidden grows the hidden slice to at least n
// entries, padding with false.
func (l *Loop) ensureHidden(n int) {
	if l.hidden == nil {
		l.hidden = make([]bool, n)
		return
	}
	for len(l.hidden) < n {
		l.hidden = append(l.hidden, false)
	}
}

// findOldestEvictable returns the index of the oldest
// (lowest) l.Messages entry that is not system and not
// already hidden. Returns -1 if there is nothing
// evictable.
func (l *Loop) findOldestEvictable() int {
	for i, m := range l.Messages {
		if m.Role == llm.RoleSystem {
			continue
		}
		if i < len(l.hidden) && l.hidden[i] {
			continue
		}
		return i
	}
	return -1
}
