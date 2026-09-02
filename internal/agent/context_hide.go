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
// entries with a single placeholder user message:
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
	l.persistProjection(context.Background())
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
		if isConversationUserTurn(m) {
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
	if hidden > 0 {
		// The turns whose reasoning was retained are gone: stale
		// thinking must not ride into the fresh conversation.
		l.lastThinking = ""
		l.persistProjection(context.Background())
	}
	return hidden
}

// VisibleMessages returns l.Messages with consecutive runs
// of hidden entries collapsed into a single placeholder. When
// nothing is hidden it returns a read-only view of l.Messages so
// the dominant provider path does not copy the full conversation.
// The placeholder is a user-role message (strict chat
// templates reject mid-history system messages); the model
// cannot tell where in the original conversation the
// cleared range was.
func (l *Loop) VisibleMessages() []llm.Message {
	if l.hidden == nil {
		return l.Messages
	}
	out := make([]llm.Message, 0, len(l.Messages))
	runStart := -1
	flush := func(end int) {
		if runStart < 0 {
			return
		}
		n := end - runStart
		out = append(out, llm.Message{
			// User role, not system: mid-conversation system messages
			// are rejected outright by some chat templates (Qwen3.5).
			Role: llm.RoleUser,
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
// VisibleMessages() with the calibrated llm.EstimateTokens
// heuristic (non-whitespace bytes / 3 + per-message framing).
// Good enough for budget-based eviction and compaction
// triggers; not for billing. The common append-only path keeps
// an exact running sum. Hidden histories use the exact full scan
// because placeholder merging makes incremental accounting rare
// and substantially more error-prone.
func (l *Loop) EstimateVisibleTokens() int {
	if l.hidden != nil {
		return llm.EstimateTokens(l.VisibleMessages())
	}
	if l.visibleEstimateCount > len(l.Messages) {
		l.invalidateVisibleEstimate()
	}
	for i := l.visibleEstimateCount; i < len(l.Messages); i++ {
		l.visibleEstimateTokens += llm.EstimateMessageTokens(l.Messages[i])
	}
	l.visibleEstimateCount = len(l.Messages)
	return l.visibleEstimateTokens
}

// invalidateVisibleEstimate must be called whenever an already-counted
// message is replaced or edited. Plain appends deliberately do not call it.
func (l *Loop) invalidateVisibleEstimate() {
	l.visibleEstimateCount = 0
	l.visibleEstimateTokens = 0
}

// EvictForBudget hides the oldest non-system messages
// until the visible token estimate drops at or below 80%
// of the F7 per-session cap. Returns the number of
// messages evicted. Emits a MessagesHiddenEvent when
// anything was evicted.
//
// When CreditTracker is nil, does not expose a cap, or the
// per-session cap is 0 (unlimited), this is a no-op.
func (l *Loop) EvictForBudget(ctx context.Context, out chan<- Event) (evicted int) {
	if l.creditTracker == nil {
		return 0
	}
	capper, ok := l.creditTracker.(sessionCapper)
	if !ok {
		return 0
	}
	sessionCap := capper.SessionCap()
	if sessionCap <= 0 {
		return 0 // unlimited: never evict for budget
	}
	threshold := int64(float64(sessionCap) * 0.8)
	if threshold <= 0 {
		return 0
	}
	// Single pass, oldest first: price the visible history ONCE,
	// then subtract each evicted message's own estimate from a
	// running total. The old form re-ran EstimateVisibleTokens()
	// (O(n)) on every iteration — O(n²) on a mass evict.
	est := l.EstimateVisibleTokens()
	if est > int(threshold) {
		l.ensureHidden(len(l.Messages))
		for i, m := range l.Messages {
			if est <= int(threshold) {
				break
			}
			if m.Role == llm.RoleSystem || l.hidden[i] {
				continue
			}
			l.hidden[i] = true
			evicted++
			est -= llm.EstimateMessageTokens(m)
			if evicted == 1 {
				// The new hidden run costs one collapsed placeholder
				// in VisibleMessages(); charge it once so the running
				// estimate stays conservative.
				est += llm.EstimateMessageTokens(llm.Message{
					Role:    llm.RoleUser,
					Content: "[earlier context cleared — 999999 message(s) compacted]",
				})
			}
		}
	}
	// One final exact control: the running estimate can drift from
	// VisibleMessages() by a placeholder merge or two. In practice
	// this loop runs zero or one iteration.
	for evicted > 0 && l.EstimateVisibleTokens() > int(threshold) {
		idx := l.findOldestEvictable()
		if idx < 0 {
			break
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
	if evicted > 0 {
		l.persistProjection(context.Background())
	}
	return evicted
}

// sessionCapper is optionally implemented by CreditTrackers
// that know their per-session token cap. EvictForBudget only
// evicts when the cap is known and positive; trackers without
// a cap (or with cap 0 = unlimited) never trigger eviction.
type sessionCapper interface {
	SessionCap() int64
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

// resetHidden clears the hidden map. Called ONLY where the
// message indices the flags refer to become invalid: after
// compaction rewrites l.Messages and when LoadConversation
// replaces the conversation body. Hides otherwise persist
// across Runs — /clear, hide_messages and budget eviction are
// durable user/budget intent, not per-Run state.
func (l *Loop) resetHidden() {
	l.hidden = nil
	l.invalidateVisibleEstimate()
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
