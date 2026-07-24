package agent

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"supercli/internal/llm"
)

func (l *Loop) maybeAutoCompact(ctx context.Context, out chan<- Event, reason string) {
	window := l.windowResolution()
	w := window.Tokens
	threshold := autoCompactThreshold(w)
	estimate := l.nextRequestTokenEstimate()
	est := estimate.Effective
	if reason == "" && est <= threshold {
		return
	}
	all := l.AllMessages()
	split := compactSplit(all, w)
	if reason == "" {
		// Speculative auto-compaction must never summarize the user turn that
		// is still running. A conservative/unknown window (the 16k fallback)
		// can otherwise compact the same long tool-heavy turn repeatedly and
		// make the agent resume from its own lossy summary. Only a real
		// provider context-limit error is allowed to use the full-history
		// "big hammer" selected by compactSplit.
		split = autoCompactSplit(all)
	}
	keep := leadingSystemCount(all)
	if split <= keep {
		return
	}
	if reason == "" && !hasFreshCompactablePrefix(all, split) {
		// Fixed request overhead (system prompt/tool schemas) can itself sit
		// above the reserve boundary. Re-summarizing an existing summary cannot reduce that
		// overhead and would otherwise spend one model call every step.
		return
	}
	removed := 0
	if l.summarizer != nil {
		// The summary is a model call: label it and book its wall
		// time as model:compact, so context_prepare (which wraps
		// this whole function on the pre-call path) keeps measuring
		// pure CLI overhead.
		sumStart := time.Now()
		summary, err := l.summarizer(llm.WithPurpose(ctx, llm.PurposeCompact), l.provider, all[:split])
		l.recordAuxWall(llm.PurposeCompact, time.Since(sumStart))
		if err == nil && summary != "" && compactionReduces(all[:split], summary) {
			removed = l.CompactPrefixWithSummary(summary, split)
		}
	}
	if removed == 0 {
		// Fallback: no summarizer or it failed (possibly
		// because the summarization call itself overflows).
		// Keep two recent user turns verbatim even when the summary provider is
		// unavailable. This preserves the active task and its latest correction.
		removed = l.HideLastUserTurns(2)
	}
	if removed == 0 {
		return
	}
	if reason == "" {
		reason = "auto"
	}
	ev := AutoCompactEvent{
		Removed: removed, Window: w, Estimated: est, RawEstimated: estimate.Raw,
		EstimateSource: estimate.Source, ExactBase: estimate.ExactBase,
		Threshold: threshold, WindowSource: window.Source,
		Reason: reason,
	}
	if out != nil {
		select {
		case out <- ev:
		case <-ctx.Done():
		}
	}
}

func hasFreshCompactablePrefix(all []llm.Message, split int) bool {
	keep := leadingSystemCount(all)
	lastUser := -1
	for i := len(all) - 1; i >= keep; i-- {
		if all[i].Role == llm.RoleUser {
			lastUser = i
			break
		}
	}
	if lastUser == keep && strings.Contains(all[keep].Content, "continued from a previous conversation that was compacted to save context") {
		// The summary is still the current turn's only user message. Tool
		// calls/results appended after it do not create older history yet.
		return false
	}
	if split > len(all) {
		split = len(all)
	}
	for _, msg := range all[keep:split] {
		if !strings.Contains(msg.Content, "continued from a previous conversation that was compacted to save context") {
			return true
		}
	}
	return false
}

// autoCompactSplit returns the start of the second-newest user turn.
// Automatic threshold-based compaction may summarize only completed older
// turns; the active turn and its immediately preceding instruction/correction
// survive byte-for-byte. When fewer than three user turns exist, it returns the
// leading-system boundary and the caller skips speculative compaction. A real
// context-limit error still uses compactSplit for emergency recovery.
func autoCompactSplit(all []llm.Message) int {
	keep := leadingSystemCount(all)
	users := 0
	for i := len(all) - 1; i >= keep; i-- {
		if all[i].Role == llm.RoleUser {
			users++
			if users == 2 {
				return i
			}
		}
	}
	return keep
}

func leadingSystemCount(all []llm.Message) int {
	keep := 0
	for keep < len(all) && all[keep].Role == llm.RoleSystem {
		keep++
	}
	return keep
}

// CompactNow performs a user-requested, summary-backed compaction immediately.
// Unlike the emergency auto path it never falls back to silently hiding
// history: if the summary call fails, the durable model projection stays
// unchanged and the UI receives the real error.
func (l *Loop) CompactNow(ctx context.Context) (AutoCompactEvent, error) {
	window := l.windowResolution()
	w := window.Tokens
	estimate := l.nextRequestTokenEstimate()
	est := estimate.Effective
	if l.summarizer == nil {
		return AutoCompactEvent{}, fmt.Errorf("manual compaction is unavailable: no summarizer")
	}
	all := l.AllMessages()
	split := compactSplit(all, w)
	keep := 0
	for keep < len(all) && all[keep].Role == llm.RoleSystem {
		keep++
	}
	if split <= keep {
		return AutoCompactEvent{}, fmt.Errorf("nothing to compact")
	}
	summary, err := l.summarizer(llm.WithPurpose(ctx, llm.PurposeCompact), l.provider, all[:split])
	if err != nil {
		return AutoCompactEvent{}, fmt.Errorf("summarize context: %w", err)
	}
	if strings.TrimSpace(summary) == "" {
		return AutoCompactEvent{}, fmt.Errorf("summarize context: empty summary")
	}
	if !compactionReduces(all[:split], summary) {
		before := llm.EstimateTokens(all[:split])
		after := llm.EstimateMessageTokens(llm.Message{Role: llm.RoleUser, Content: summary})
		return AutoCompactEvent{}, fmt.Errorf("summarize context: insufficient reduction (%d -> %d tokens)", before, after)
	}
	removed := l.CompactPrefixWithSummary(summary, split)
	if removed == 0 {
		return AutoCompactEvent{}, fmt.Errorf("nothing to compact")
	}
	return AutoCompactEvent{
		Removed: removed, Window: w, Estimated: est, RawEstimated: estimate.Raw,
		EstimateSource: estimate.Source, ExactBase: estimate.ExactBase,
		Threshold: autoCompactThreshold(w), WindowSource: window.Source,
		Reason: "manual",
	}, nil
}

func compactionReduces(prefix []llm.Message, summary string) bool {
	before := llm.EstimateTokens(prefix)
	if before <= 0 {
		return false
	}
	after := llm.EstimateMessageTokens(llm.Message{Role: llm.RoleUser, Content: summary})
	return float64(after) <= float64(before)*maxCompactionRatio
}

// compactSplit picks the summarization cut for compaction: the start of the
// second-newest user turn, so two recent turns survive verbatim
// (a small model resumes far better from its own recent messages than
// from a summary of them). Falls back to the full length — the old
// replace-everything behaviour — when there is nothing meaningful
// before the last turn, or when the last turn alone would still eat
// more than half the window (a single huge turn needs the big hammer).
func compactSplit(all []llm.Message, window int) int {
	keep := leadingSystemCount(all)
	split := -1
	users := 0
	for i := len(all) - 1; i >= keep; i-- {
		if all[i].Role == llm.RoleUser {
			users++
			if users == 2 {
				split = i
				break
			}
		}
	}
	if split <= keep {
		// With too little history there is nothing safe to summarize. A real
		// overflow caused by one giant recent turn is handled by the size check
		// below using the latest user boundary.
		for i := len(all) - 1; i >= keep; i-- {
			if all[i].Role == llm.RoleUser {
				split = i
				break
			}
		}
		if split <= keep {
			return keep
		}
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
