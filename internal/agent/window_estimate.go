package agent

import (
	"context"
	"strings"

	"supercli/internal/llm"
)

func (l *Loop) estimateNextRequestTokensRaw() int {
	if l.route != RouteCoordinator {
		return l.estimateChatRequestTokensRaw()
	}
	visible := l.VisibleMessages()
	projected := l.resolvedToolProviderView(visible)
	est := l.EstimateVisibleTokens()
	if len(projected) != len(visible) {
		est = llm.EstimateTokens(projected)
	}
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

func estimateRequestTokens(msgs []llm.Message, defs []llm.ToolDef) int {
	b := llm.EstimateRequestBreakdown(msgs, defs)
	return b.System + b.User + b.Assistant + b.Tool + b.Other
}

// estimateChatRequestTokensRaw mirrors the light-route projection without
// mutating chatWindowStart. The previous full-history estimate was calibrated
// against a much smaller provider-reported prompt, so switching back to the
// coordinator could turn a 100k request into an apparent 5k request.
func (l *Loop) estimateChatRequestTokensRaw() int {
	visible := l.resolvedToolProviderView(l.VisibleMessages())
	system := chatOnlySystemPrompt
	if l.route == RouteAdvisor || l.route == RouteClarify {
		system = advisorSystemPrompt
	}
	if l.briefing != "" {
		system += "\n\n" + l.briefing
	}
	msgs := []llm.Message{{Role: llm.RoleSystem, Content: system}}
	lastUser := -1
	for i := len(visible) - 1; i >= 0; i-- {
		if visible[i].Role == llm.RoleUser && !strings.Contains(visible[i].Content, "<task-notification>") {
			lastUser = i
			break
		}
	}
	end := len(visible)
	if lastUser >= 0 {
		end = lastUser
	}
	start := l.chatWindowStart
	if start < 0 || start > end {
		start = 0
	}
	window := make([]llm.Message, 0, end-start)
	for i := start; i < end; i++ {
		if chatWindowEligible(visible[i]) {
			window = append(window, visible[i])
		}
	}
	if llm.EstimateTokens(window) > chatWindowMaxTokens {
		kept := 0
		for i := end - 1; i >= start && kept < chatWindowKeepMsgs; i-- {
			if chatWindowEligible(visible[i]) {
				kept++
			}
		}
		if kept < len(window) {
			window = window[len(window)-kept:]
		}
	}
	msgs = append(msgs, window...)
	if lastUser >= 0 {
		for _, msg := range visible[lastUser:] {
			if msg.Role != llm.RoleSystem {
				msgs = append(msgs, msg)
			}
		}
	}
	msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: l.stampSection()})
	var defs []llm.ToolDef
	if l.registry != nil {
		defs = l.buildToolDefs()
	}
	return estimateRequestTokens(msgs, defs)
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

func (l *Loop) recordContextBaseline(estimated, exact int) {
	if estimated <= 0 || exact <= 0 {
		return
	}
	l.sessUsageMu.Lock()
	l.contextBaseEstimated = estimated
	l.contextBaseExact = exact
	l.contextBaseRoute = l.route
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
// visible token estimate enters the model's reserved generation
// space, the conversation is summarized (via the same
// machinery as /compact) and replaced. Emits AutoCompactEvent
// so the TUI can show a marker. Best-effort: a summarization
// failure falls back to hiding all but the two most recent user turns.
