package agent

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"supercli/internal/llm"
)

// defaultContextWindow is the conservative fallback when the
// model's context window cannot be resolved from config,
// provider metadata, or learned limits.
const defaultContextWindow = 16384

// autoCompactThreshold: compact when the visible token
// estimate exceeds this fraction of the context window.
const autoCompactThreshold = 0.8

// Summarizer produces a compaction summary (already wrapped in
// any resume framing) of msgs using provider. main.go wires the
// /compact machinery (compact.go) here.
type Summarizer func(ctx context.Context, p llm.Provider, msgs []llm.Message) (string, error)

// window resolves the context window for the loop's current
// model. The WindowFor callback owns the cascade (config >
// provider metadata > learned > default); the loop only guards
// against a missing or nonsensical callback.
func (l *Loop) window() int {
	if l.windowFor != nil {
		if v := l.windowFor(l.modelID); v > 0 {
			return v
		}
	}
	return defaultContextWindow
}

// ContextWindow exposes the resolved window (config > provider
// metadata > learned > default) so front-ends and tests can verify
// the WindowFor wiring actually reached the loop.
func (l *Loop) ContextWindow() int {
	return l.window()
}

// maybeAutoCompact runs before every provider call. When the
// visible token estimate exceeds 80% of the model's context
// window, the conversation is summarized (via the same
// machinery as /compact) and replaced. Emits AutoCompactEvent
// so the TUI can show a marker. Best-effort: a summarization
// failure falls back to hiding all but the last user turn.
func (l *Loop) maybeAutoCompact(ctx context.Context, out chan<- Event, reason string) {
	w := l.window()
	est := l.EstimateVisibleTokens()
	if reason == "" && float64(est) <= autoCompactThreshold*float64(w) {
		return
	}
	removed := 0
	if l.summarizer != nil {
		all := l.AllMessages()
		split := compactSplit(all, w)
		if summary, err := l.summarizer(ctx, l.provider, all[:split]); err == nil && summary != "" {
			removed = l.CompactPrefixWithSummary(summary, split)
		}
	}
	if removed == 0 {
		// Fallback: no summarizer or it failed (possibly
		// because the summarization call itself overflows).
		// Hide everything except the most recent user turn.
		removed = l.HideLastUserTurns(1)
	}
	if removed == 0 {
		return
	}
	if reason == "" {
		reason = "auto"
	}
	ev := AutoCompactEvent{Removed: removed, Window: w, Estimated: est, Reason: reason}
	if out != nil {
		select {
		case out <- ev:
		case <-ctx.Done():
		}
	}
}

// compactSplit picks the summarization cut for auto-compaction: the
// start of the last user turn, so the current turn survives verbatim
// (a small model resumes far better from its own recent messages than
// from a summary of them). Falls back to the full length — the old
// replace-everything behaviour — when there is nothing meaningful
// before the last turn, or when the last turn alone would still eat
// more than half the window (a single huge turn needs the big hammer).
func compactSplit(all []llm.Message, window int) int {
	keep := 0
	for keep < len(all) && all[keep].Role == llm.RoleSystem {
		keep++
	}
	split := -1
	for i := len(all) - 1; i >= keep; i-- {
		if all[i].Role == llm.RoleUser {
			split = i
			break
		}
	}
	if split <= keep {
		return len(all)
	}
	if llm.EstimateTokens(all[split:]) > window/2 {
		return len(all)
	}
	return split
}

// contextLimitPhrases are matched (lowercased) against provider
// errors to detect a context-length overflow.
var contextLimitPhrases = []string{
	"maximum context length",
	"context_length_exceeded",
	"too many tokens",
	"context length",
}

// isContextLimitErr reports whether err looks like a provider
// context-window overflow.
func isContextLimitErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, p := range contextLimitPhrases {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

// contextLimitRe extracts the advertised limit from messages
// like "This model's maximum context length is 8192 tokens" or
// "context length of only 4096 tokens".
var contextLimitRe = regexp.MustCompile(`(?i)context length (?:is |of (?:only )?)?(\d+)`)

// extractContextLimit pulls the numeric context limit out of a
// provider error message. Returns 0 when absent.
func extractContextLimit(msg string) int {
	m := contextLimitRe.FindStringSubmatch(msg)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// handleContextOverflow reacts to a context-length error from
// the provider: persist the learned limit (if the message
// carries a number), compact, and tell the caller to retry
// once. Returns false when err is not a context-limit error.
func (l *Loop) handleContextOverflow(ctx context.Context, err error, out chan<- Event) bool {
	if !isContextLimitErr(err) {
		return false
	}
	if lim := extractContextLimit(err.Error()); lim > 0 && l.learnLimit != nil {
		l.learnLimit(l.modelID, lim)
	}
	l.maybeAutoCompact(ctx, out, "context-limit")
	return true
}
