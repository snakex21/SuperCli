package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"supercli/internal/agent/ultrawork"
	"supercli/internal/llm"
	"supercli/internal/system/stats"
)

func (l *Loop) completeOnce(ctx context.Context, toolDefs []llm.ToolDef, out chan<- Event) (string, []llm.ToolCall, *llm.Usage, error) {
	l.nativeReasoning = nil
	// context_prepare part 2: provider message assembly (visible
	// view, thin preamble placement, freshness stamp).
	msgStart := time.Now()
	msgs, messageEstimate := l.prepareProviderMessages(true)
	// Calibrate against the same local projection used by the NEXT compaction
	// check. Chat/advisor routes intentionally send a smaller history window;
	// comparing that wire-only estimate with the full logical estimate on the
	// next turn would manufacture a large delta that was never appended.
	requestEstimate := messageEstimate + estimateRequestTokens(nil, toolDefs)
	l.recordWallPhase(stats.PhaseContextPrepare, time.Since(msgStart))
	window := l.windowResolution()
	hardThreshold := autoCompactThreshold(window.Tokens)
	effectiveThreshold, thresholdSource := l.effectiveCompactThreshold(hardThreshold, window.Source)
	ctx = llm.WithPrefillBudget(ctx, effectiveThreshold, thresholdSource)

	// request_encode: provider.Complete up to the stream handoff —
	// request serialization plus whatever the provider does before
	// returning its delta channel.
	encStart := time.Now()
	stream, err := l.provider.Complete(ctx, msgs, toolDefs)
	l.recordWallPhase(stats.PhaseRequestEncode, time.Since(encStart))
	if err != nil {
		return "", nil, nil, fmt.Errorf("agent: provider.Complete: %w", err)
	}
	// Provider accepted a request snapshot containing any active images. Mark
	// the live-history refs dormant immediately so later steps/turns carry only
	// MediaID handles unless the model explicitly reloads one.
	l.deactivateActiveImages()
	text, calls, usage, err := l.consume(ctx, stream, out)
	if err == nil && usage != nil && usage.Output > 0 && l.lastCallGeneration >= time.Millisecond {
		l.generationTokens += usage.Output
		l.generationDuration += l.lastCallGeneration
	}
	if err == nil && usage != nil {
		l.recordContextBaseline(requestEstimate, usage.Input)
	}
	if err == nil {
		// Light chat routes send only a small view. They must not mark the
		// full coordinator history as consumed by the new model.
		if l.route == RouteCoordinator {
			l.rememberContextModel(ctx)
		}
		l.observePrefillCall(requestEstimate, usage, l.lastCallTTFT)
	}
	return text, calls, usage, err
}

// stampSection is the per-request trailing prompt content: the
// freshness timestamp plus, when the user has turned thinking off for a
// model that honours a prompt soft switch (Qwen /no_think), the
// suppression token. Both live at the very END of the prompt so the
// cacheable prefix is undisturbed — the token is append-only exactly
// like the timestamp, and toggling it never rewrites earlier bytes.
func (l *Loop) stampSection() string {
	s := timeSection(time.Now())
	if d := llm.ThinkingDirective(l.modelID); d != "" {
		s += "\n\n" + d
	}
	return s
}

func (l *Loop) providerMessages() []llm.Message {
	out, _ := l.prepareProviderMessages(false)
	return out
}

// prepareProviderMessages optionally prices the exact request view as it is
// assembled. Unchanged canonical history reuses the append-only estimate;
// projected histories retain a full scan. The wire payload is identical.
func (l *Loop) prepareProviderMessages(estimate bool) ([]llm.Message, int) {
	if l.route == RouteCoordinator {
		// Per-request freshness stamp: appended at the END so the stable
		// prompt prefix stays cacheable by the provider.
		visible := l.resolvedToolProviderView(llm.ProjectReasoningHistory(l.provider, l.reasoningHistoryView(l.VisibleMessages())))
		tokens := 0
		cachedHistory := estimate && l.hidden == nil && len(visible) == len(l.Messages) &&
			(len(visible) == 0 || &visible[0] == &l.Messages[0])
		out := make([]llm.Message, 0, len(visible)+2)
		// Thin tool protocol placement depends on stableToolset:
		//
		// stableToolset + catalogHoist: the catalog is byte-stable all
		// session (activated tools stay in the tail), so the preamble
		// is HOISTED into the stable prompt prefix — right after the
		// leading run of system messages. There it sits in the
		// server-side KV cache and is evaluated once, instead of being
		// re-injected behind the growing history (a position that
		// shifts every step, forcing llama.cpp to re-eval the whole
		// catalog on every call). The rendered bytes are frozen on
		// first use so late registry changes cannot silently rewrite
		// the prefix. The hoisted message never enters l.Messages, so
		// prune/compaction/token accounting never see it.
		//
		// stableToolset OFF (or hoist not enabled): the catalog can
		// change on activation / recency is preferred, so keep the
		// historical placement — injected just before the freshness
		// stamp at the end of the prompt.
		if l.stableToolset && l.catalogHoist {
			if !l.hoistedPreSet {
				l.hoistedPre = l.thinToolsPreamble()
				l.hoistedPreSet = true
			}
			lead := 0
			for lead < len(visible) && visible[lead].Role == llm.RoleSystem {
				lead++
			}
			// Strict llama.cpp templates commonly accept exactly ONE system
			// message, at index zero. A separate hoisted system message made
			// those templates reject the request before inference. Merge the
			// entire leading system run and the frozen preamble into one stable
			// message; the bytes remain append-only and cacheable, which is the
			// purpose of the hoist in the first place.
			leading := make([]string, 0, lead+1)
			for _, msg := range visible[:lead] {
				// Media markers must exist before merging leading text. Even an
				// active image invalidates the canonical-history estimate.
				if parts := projectedMediaProviderParts(msg.Parts, true); parts != nil {
					msg.Parts = parts
					cachedHistory = false
				}
				if cachedHistory {
					tokens -= llm.EstimateMessageTokens(msg)
				}
				if text := messageDraftText(msg); text != "" {
					leading = append(leading, text)
				}
			}
			if l.hoistedPre != "" {
				leading = append(leading, l.hoistedPre)
			}
			if len(leading) > 0 {
				out = append(out, llm.Message{Role: llm.RoleSystem, Content: strings.Join(leading, "\n\n")})
				if cachedHistory {
					tokens += llm.EstimateMessageTokens(out[len(out)-1])
				}
			}
			out = append(out, visible[lead:]...)
		} else {
			out = append(out, visible...)
			if pre := l.thinToolsPreamble(); pre != "" {
				out = append(out, llm.Message{Role: llm.RoleSystem, Content: pre})
				if cachedHistory {
					tokens += llm.EstimateMessageTokens(out[len(out)-1])
				}
			}
		}
		out = append(out, llm.Message{Role: llm.RoleSystem, Content: l.trailingContext()})
		if cachedHistory {
			tokens += llm.EstimateMessageTokens(out[len(out)-1])
		}
		if projectOwnedMediaProviderMessages(out) {
			cachedHistory = false
		}
		if cachedHistory {
			tokens += l.EstimateVisibleTokens()
		}
		if estimate && !cachedHistory {
			tokens = llm.EstimateTokens(out)
		}
		return out, tokens
	}
	visible := l.resolvedToolProviderView(llm.ProjectReasoningHistory(l.provider, l.reasoningHistoryView(l.VisibleMessages())))
	system := chatOnlySystemPrompt
	if l.route == RouteAdvisor || l.route == RouteClarify {
		system = advisorSystemPrompt
	}
	// Memory briefing must survive the route switch: the chat-only
	// prompt replaces the full system prompt, but durable user
	// facts (name, language, preferences) still apply to smalltalk.
	if l.briefing != "" {
		system += "\n\n" + l.briefing
	}
	out := []llm.Message{{Role: llm.RoleSystem, Content: system}}

	window, _, nextStart := chatHistoryProjectionWithMediaCost(visible, l.chatWindowStart, false, true)
	l.chatWindowStart = nextStart
	out = append(out, window...)
	// Per-request freshness stamp at the very END, same pattern as the
	// coordinator route: the minute-granular stamp used to be baked into
	// the leading system prompt, rewriting the prompt front every minute
	// and killing the provider-side KV cache. The provider demote pass
	// renders this trailing system message in place as a
	// <system-reminder> user turn.
	out = append(out, llm.Message{Role: llm.RoleSystem, Content: l.trailingContext()})
	projectOwnedMediaProviderMessages(out)
	if estimate {
		return out, llm.EstimateTokens(out)
	}
	return out, 0
}

// trailingContext appends live context and freshness after the conversation.
// Reasoning is included only with explicit legacy opt-in. This request-only
// tail is never persisted as part of the user's transcript.
func (l *Loop) trailingContext() string {
	s := l.contextTail()

	if l.finalReplyOnly {
		return s + "\n\n[final reply only] The requested Word operation succeeded. " +
			"Do not call or describe another tool. Briefly tell the user that the document is ready, include its path, and stop."
	}
	if l.keepThinking && l.lastThinking != "" {
		s += "\n\n[Retained reasoning from your previous turn — treat it as your own chain of thought and continue from it; do not reveal it to the user:]\n" + l.lastThinking
	}
	return s
}

// contextTail keeps refreshed snapshots behind the conversation. Updating a
// remembered fact must not invalidate the entire conversation prefix. Reuse
// the existing trailing message so this adds no message or instruction wrapper.
// The shared tail is also priced by the context-window estimator.
func (l *Loop) contextTail() string {
	s := l.stampSection()
	if l.liveContext != "" {
		s = l.liveContext + "\n\n" + s
	}
	if l.ultraworkMode {
		s += ultrawork.SystemPromptSection()
		if l.ultraworkReminder != "" {
			s += "\n\n" + l.ultraworkReminder
		}
	}
	if requested := l.requestedToolContext(); requested != "" {
		s += "\n\n" + requested
	}
	return s
}
