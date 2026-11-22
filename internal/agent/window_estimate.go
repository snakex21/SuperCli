package agent

import (
	"context"

	"supercli/internal/llm"
)

func (l *Loop) estimateNextRequestTokensRaw() int {
	if l.route != RouteCoordinator {
		return l.estimateChatRequestTokensRaw()
	}
	visible := l.VisibleMessages()
	projected := l.resolvedToolProviderView(llm.ProjectReasoningHistory(l.provider, l.reasoningHistoryView(visible)))
	var est int
	// Reuse the append-only cache only for unchanged history. Otherwise price
	// the prepared view directly, without rebuilding a hidden view or counting
	// reasoning/tool evidence that the request projection has removed.
	if l.hidden == nil && len(projected) == len(visible) &&
		(len(projected) == 0 || &projected[0] == &visible[0]) {
		est = l.EstimateVisibleTokens()
	} else {
		est = llm.EstimateTokens(projected)
	}
	if l.registry == nil {
		return est
	}
	est += l.toolDefinitionTokens()
	if l.route == RouteCoordinator {
		if pre := l.thinToolsPreamble(); pre != "" {
			est += llm.EstimateMessageTokens(llm.Message{Role: llm.RoleSystem, Content: pre})
		}
		est += llm.EstimateMessageTokens(llm.Message{Role: llm.RoleSystem, Content: l.contextTail()})
	}
	return est
}

func estimateRequestTokens(msgs []llm.Message, defs []llm.ToolDef) int {
	b := llm.EstimateRequestBreakdown(msgs, defs)
	return b.System + b.User + b.Assistant + b.Tool + b.Other
}

// estimateChatRequestTokensRaw mirrors the light-route projection without
// mutating chatWindowStart. The previous full-history estimate was calibrated
// against a much smaller provider-reported prompt, so switching back to the
// coordinator could turn a 100k request into an apparent 5k request.
func (l *Loop) estimateChatRequestTokensRaw() int {
	visible := l.resolvedToolProviderView(llm.ProjectReasoningHistory(l.provider, l.reasoningHistoryView(l.VisibleMessages())))
	system := chatOnlySystemPrompt
	if l.route == RouteAdvisor || l.route == RouteClarify {
		system = advisorSystemPrompt
	}
	if l.briefing != "" {
		system += "\n\n" + l.briefing
	}
	system = systemWithUserInstructions(system, l.userInstructions)
	history, _, _ := chatHistoryProjection(visible, l.chatWindowStart, false)
	est := llm.EstimateTokens(history)
	est += llm.EstimateMessageTokens(llm.Message{Role: llm.RoleSystem, Content: system})
	est += llm.EstimateMessageTokens(llm.Message{Role: llm.RoleSystem, Content: l.contextTail()})
	if l.registry != nil {
		est += l.toolDefinitionTokens()
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
	exact, base, baseRoute := l.contextBaseExact, l.contextBaseEstimated, l.contextBaseRoute
	l.sessUsageMu.Unlock()
	// Add only the locally estimated growth since the last exact request.
	// A smaller raw estimate means the projection changed (compact/prune/hide
	// or route reset), so applying the old baseline would be invalid.
	if exact > 0 && base > 0 && baseRoute == l.route && raw >= base {
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

func (l *Loop) recordContextBaseline(estimated, exact int) (truncated bool) {
	if estimated <= 0 || exact <= 0 {
		return false
	}
	window := l.windowResolution().Tokens
	l.sessUsageMu.Lock()
	defer l.sessUsageMu.Unlock()
	if serverTruncatedPrompt(l.contextBaseEstimated, l.contextBaseExact, estimated, exact) && l.contextBaseRoute == l.route &&
		(bigDrop(l.contextBaseExact, exact) || nearWindow(l.contextBaseExact, window)) {
		// Keep the last trustworthy pair: the next estimate is then the old
		// exact count plus local growth, i.e. what was really sent.
		l.serverTruncated = true
		return true
	}
	l.contextBaseEstimated = estimated
	l.contextBaseExact = exact
	l.contextBaseRoute = l.route
	return false
}

func (l *Loop) takeServerTruncated() bool {
	l.sessUsageMu.Lock()
	defer l.sessUsageMu.Unlock()
	truncated := l.serverTruncated
	l.serverTruncated = false
	return truncated
}

// serverTruncatedPrompt recognizes a server that silently dropped part of an
// over-long prompt instead of rejecting it. LM Studio (context overflow
// policy truncateMiddle/rollingWindow) and Ollama do this when prompt plus
// generation exceed the loaded n_ctx, and report the SHORTENED prompt as
// usage. The local projection only grew, yet the provider count fell.
//
// Measured on LM Studio qwen3.6-35b-a3b at 32k: after a request of 32248
// prompt tokens the next one (larger) reported 16022. Trusting that number as
// the exact baseline let the loop believe it had 16k of room; 15 further
// requests were truncated by the server, each re-evaluating ~13.9k tokens
// (71.7 s of prompt eval in one task) while the model lost the middle of
// its own work.
//
// A shrink beyond 2% while the local estimate did not shrink is truncation.
// Compaction, pruning and hiding all shrink the local estimate as well.
//
// A server only cuts a prompt that no longer fits its window, so the previous
// request must have filled a good part of it. Hosted APIs report input counts
// that wobble by a few percent between requests (observed on a Responses
// backend: 52277 then 50904 tokens for a larger request in a 500k window);
// far below the window that is not truncation, and treating it as such forced
// a compaction that aborted the turn with no answer.
//
// The resolved window is not always the server's: before Ollama or LM Studio
// load a model it comes from metadata (e.g. 128k) while the server runs at
// 4-32k. A real cut is large (32248 -> 16022, about 50%), hosted jitter is
// small (about 2.6%), so a drop of 15% or more counts at any window size.
func nearWindow(exact, window int) bool {
	return window <= 0 || exact*2 >= window
}

func bigDrop(prevExact, exact int) bool {
	return prevExact > 0 && (prevExact-exact)*100 >= prevExact*15
}

func serverTruncatedPrompt(prevEstimated, prevExact, estimated, exact int) bool {
	if prevEstimated <= 0 || prevExact <= 0 || estimated < prevEstimated {
		return false
	}
	return exact < prevExact-prevExact/50
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
// visible token estimate enters the model's reserved generation
// space, the conversation is summarized (via the same
// machinery as /compact) and replaced. Emits AutoCompactEvent
// so the TUI can show a marker. A configured summarizer's failure preserves
// history and stops the turn; only loops without one use the legacy hide fallback.
