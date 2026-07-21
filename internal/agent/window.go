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

// defaultContextWindow is the conservative fallback when the
// model's context window cannot be resolved from config,
// provider metadata, or learned limits.
const defaultContextWindow = 16384

// autoCompactThreshold: compact when the visible token
// estimate exceeds this fraction of the context window.
const autoCompactThreshold = 0.8

// A generated summary must remove at least 20% of the prefix it replaces.
// Otherwise accepting it spends context while also discarding exact history.
// The threshold mirrors the proven guard in grok-build and is deliberately
// fixed: it is a safety invariant, not another user-facing tuning knob.
const maxCompactionRatio = 0.8

// ContextWindowResolution records both the limit and why SuperCli trusts it.
// The source is intentionally a short stable string because it is emitted in
// TUI/WebGUI/batch telemetry and may be grepped from session logs.
type ContextWindowResolution struct {
	Tokens int
	Source string
}

// ResolveContextWindow applies the shared front-end cascade. providerTokens is
// the optional live /v1/models result; callers that do not have one pass zero.
func ResolveContextWindow(model string, configured, providerTokens int, caps *llm.CapabilityRegistry, learned *llm.LearnedLimits) ContextWindowResolution {
	if configured > 0 {
		return ContextWindowResolution{Tokens: configured, Source: "config"}
	}
	if providerTokens > 0 {
		return ContextWindowResolution{Tokens: providerTokens, Source: "provider"}
	}
	if caps != nil {
		if info, ok := caps.Get(model); ok && info.ContextLength > 0 {
			return ContextWindowResolution{Tokens: info.ContextLength, Source: "catalog"}
		}
		// Router providers often use a short active id while catalogs store a
		// canonical provider/model id. Accept only a unique suffix match so two
		// providers advertising the same short id can never select each other.
		short := model
		if slash := strings.LastIndexByte(short, '/'); slash >= 0 {
			short = short[slash+1:]
		}
		matched := 0
		matches := 0
		for _, info := range caps.All() {
			candidate := info.ID
			if slash := strings.LastIndexByte(candidate, '/'); slash >= 0 {
				candidate = candidate[slash+1:]
			}
			if strings.EqualFold(candidate, short) && info.ContextLength > 0 {
				matches++
				if matches > 1 {
					matched = 0
					break
				}
				matched = info.ContextLength
			}
		}
		if matched > 0 {
			return ContextWindowResolution{Tokens: matched, Source: "catalog-alias"}
		}
	}
	if learned != nil {
		if tokens := learned.Get(model); tokens > 0 {
			return ContextWindowResolution{Tokens: tokens, Source: "learned"}
		}
	}
	return ContextWindowResolution{}
}

// EstimateNextRequestTokens prices the complete prompt that is about to reach
// the provider, not only the persisted conversation. Tool definitions, the
// thin-tool catalog and the per-request stamp all consume the same context
// window. Keeping them in the trigger leaves the remaining 20% of the window
// available for generation instead of discovering the real size at the
// provider boundary.
func (l *Loop) estimateNextRequestTokensRaw() int {
	est := l.EstimateVisibleTokens()
	if l.registry == nil {
		return est
	}
	defs := l.buildToolDefs()
	toolCost := llm.EstimateRequestBreakdown(nil, defs)
	est += toolCost.System + toolCost.User + toolCost.Assistant + toolCost.Tool + toolCost.Other
	if l.route == RouteCoordinator {
		if pre := l.thinToolsPreamble(); pre != "" {
			est += llm.EstimateMessageTokens(llm.Message{Role: llm.RoleSystem, Content: pre})
		}
		est += llm.EstimateMessageTokens(llm.Message{Role: llm.RoleSystem, Content: l.stampSection()})
	}
	return est
}

type requestTokenEstimate struct {
	Effective     int
	Raw           int
	Source        string
	ExactBase     int
	EstimatedBase int
}

func (l *Loop) nextRequestTokenEstimate() requestTokenEstimate {
	raw := l.estimateNextRequestTokensRaw()
	out := requestTokenEstimate{Effective: raw, Raw: raw, Source: "estimate"}
	l.sessUsageMu.Lock()
	exact, base := l.contextBaseExact, l.contextBaseEstimated
	l.sessUsageMu.Unlock()
	// Add only the locally estimated growth since the last exact request.
	// A smaller raw estimate means the projection changed (compact/prune/hide
	// or route reset), so applying the old baseline would be invalid.
	if exact > 0 && base > 0 && raw >= base {
		calibrated := exact + raw - base
		if calibrated > 0 {
			out.Effective = calibrated
			out.Source = "provider+delta"
			out.ExactBase = exact
			out.EstimatedBase = base
		}
	}
	return out
}

// EstimateNextRequestTokens returns the best available prediction: provider
// usage plus estimated append-only growth when possible, otherwise the fully
// local calibrated estimator.
func (l *Loop) EstimateNextRequestTokens() int {
	return l.nextRequestTokenEstimate().Effective
}

func (l *Loop) recordContextBaseline(estimated, exact int) {
	if estimated <= 0 || exact <= 0 {
		return
	}
	l.sessUsageMu.Lock()
	l.contextBaseEstimated = estimated
	l.contextBaseExact = exact
	l.sessUsageMu.Unlock()
}

// Summarizer produces a compaction summary (already wrapped in
// any resume framing) of msgs using provider. main.go wires the
// /compact machinery (compact.go) here.
type Summarizer func(ctx context.Context, p llm.Provider, msgs []llm.Message) (string, error)

// window resolves the context window for the loop's current
// model. The WindowFor callback owns the cascade (config >
// provider metadata > learned > default); the loop only guards
// against a missing or nonsensical callback.
func (l *Loop) window() int {
	return l.windowResolution().Tokens
}

func (l *Loop) windowResolution() ContextWindowResolution {
	if l.scopedWindowFor != nil {
		if resolved := l.scopedWindowFor(l.contextProvider, l.modelID); resolved.Tokens > 0 {
			if resolved.Source == "" {
				resolved.Source = "model-override"
			}
			return resolved
		}
	}
	if l.contextWindowFor != nil {
		if resolved := l.contextWindowFor(l.modelID); resolved.Tokens > 0 {
			if resolved.Source == "" {
				resolved.Source = "callback"
			}
			return resolved
		}
	}
	if l.windowFor != nil {
		if v := l.windowFor(l.modelID); v > 0 {
			return ContextWindowResolution{Tokens: v, Source: "callback"}
		}
	}
	return ContextWindowResolution{Tokens: defaultContextWindow, Source: "fallback"}
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
	window := l.windowResolution()
	w := window.Tokens
	estimate := l.nextRequestTokenEstimate()
	est := estimate.Effective
	if reason == "" && float64(est) <= autoCompactThreshold*float64(w) {
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
		// above 80%. Re-summarizing an existing summary cannot reduce that
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
		// Hide everything except the most recent user turn.
		removed = l.HideLastUserTurns(1)
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
		Threshold: int(autoCompactThreshold * float64(w)), WindowSource: window.Source,
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

// autoCompactSplit returns the start of the latest user turn, even when that
// turn alone is large. Automatic threshold-based compaction may summarize only
// completed older turns; the active turn survives byte-for-byte. When no older
// turn exists, it returns the leading-system boundary and the caller skips
// speculative compaction. A real context-limit error still uses compactSplit,
// which may compact the active turn as an emergency recovery.
func autoCompactSplit(all []llm.Message) int {
	keep := leadingSystemCount(all)
	for i := len(all) - 1; i >= keep; i-- {
		if all[i].Role == llm.RoleUser {
			return i
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
		Threshold: int(autoCompactThreshold * float64(w)), WindowSource: window.Source,
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

// compactSplit picks the summarization cut for auto-compaction: the
// start of the last user turn, so the current turn survives verbatim
// (a small model resumes far better from its own recent messages than
// from a summary of them). Falls back to the full length — the old
// replace-everything behaviour — when there is nothing meaningful
// before the last turn, or when the last turn alone would still eat
// more than half the window (a single huge turn needs the big hammer).
func compactSplit(all []llm.Message, window int) int {
	keep := leadingSystemCount(all)
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
