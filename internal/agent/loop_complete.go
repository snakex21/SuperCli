package agent

import (
	"context"
	"fmt"
	"time"

	"supercli/internal/agent/ultrawork"
	"supercli/internal/llm"
	"supercli/internal/system/stats"
)

func (l *Loop) completeOnce(ctx context.Context, toolRequest preparedToolRequest, out chan<- Event) (string, []llm.ToolCall, *llm.Usage, error) {
	l.nativeReasoning = nil
	// context_prepare part 2: provider message assembly (visible
	// view, thin preamble placement, freshness stamp).
	msgStart := time.Now()
	msgs, messageEstimate := l.prepareProviderMessages(true)
	// Calibrate against the same local projection used by the NEXT compaction
	// check. Chat/advisor routes intentionally send a smaller history window;
	// comparing that wire-only estimate with the full logical estimate on the
	// next turn would manufacture a large delta that was never appended.
	requestEstimate := messageEstimate + toolRequest.tokens
	l.recordWallPhase(stats.PhaseContextPrepare, time.Since(msgStart))
	window := l.windowResolution()
	// A shorter speculative prefix may not free enough space after a large
	// tool result. Never send a known oversized request (including retries),
	// and never summarize the active user turn just to make it fit.
	if estimate := l.nextRequestTokenEstimate(); requestEstimate > window.Tokens || estimate.Effective > window.Tokens {
		return "", nil, nil, fmt.Errorf("agent: request exceeds context window after compaction (%d tokens, window %d); active user turn is preserved", max(requestEstimate, estimate.Effective), window.Tokens)
	}
	hardThreshold := autoCompactThreshold(window.Tokens)
	effectiveThreshold, thresholdSource := l.effectiveCompactThreshold(hardThreshold, window.Source)
	ctx = llm.WithPrefillBudget(ctx, effectiveThreshold, thresholdSource)

	// request_encode: provider.Complete up to the stream handoff —
	// request serialization plus whatever the provider does before
	// returning its delta channel.
	encStart := time.Now()
	stream, err := l.provider.Complete(ctx, msgs, toolRequest.defs)
	l.recordWallPhase(stats.PhaseRequestEncode, time.Since(encStart))
	if err != nil {
		return "", nil, nil, &providerCompleteError{cause: err, streamReturned: stream != nil}
	}
	// Provider accepted a request snapshot containing any active images. Mark
	// the live-history refs dormant immediately so later steps/turns carry only
	// MediaID handles unless the model explicitly reloads one.
	l.deactivateActiveImages()
	hadProgress := false
	text, calls, usage, err := l.consumeWithProgress(ctx, stream, out, &hadProgress)
	if err != nil {
		if _, truncated := err.(*truncatedToolResponseError); !truncated {
			// Preserve the partial transcript/calls for the caller's interrupted
			// reply retention, but never present an accepted stream as retryable
			// startup failure. Native stream resume is not supported.
			err = &acceptedStreamError{cause: err, hadProgress: hadProgress}
		}
	}
	if err == nil {
		// A hidden reasoning count without a reasoning-start signal has no
		// output-only clock. A single batched output also has no measured
		// generation interval. Never turn either into a fictitious tok/s rate.
		measured := usage != nil && l.lastCallGeneration >= time.Millisecond && l.lastCallOutputFrames > 1 &&
			(usage.Reasoning == 0 || l.lastCallReasoningStarted)
		if !measured && (usage == nil || usage.Output > 0) {
			l.generationIncomplete = true
			l.generationTokens, l.generationDuration = 0, 0
		} else if measured && usage.Output > 0 && !l.generationIncomplete {
			l.generationTokens += usage.Output
			l.generationDuration += l.lastCallGeneration
		}
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
		visible := sourceEvidenceProjection(l.resolvedToolProviderView(llm.ProjectReasoningHistory(l.provider, l.reasoningHistoryView(l.VisibleMessages()))), l.baseDir)
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
			leadingBlocks := make([]llm.ContextBlock, 0, lead+1)
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
					source := llm.ContextUnknown
					if msg.Origin != nil && len(msg.Origin.Spans) == 0 {
						source = msg.Origin.Source
					}
					leadingBlocks = append(leadingBlocks, llm.ContextBlock{Source: source, Text: text})
				}
			}
			if l.hoistedPre != "" {
				leadingBlocks = append(leadingBlocks, llm.ContextBlock{Source: llm.SourceInstructions, Text: l.hoistedPre})
			}
			if len(leadingBlocks) > 0 {
				out = append(out, llm.MessageWithContextBlocks(llm.RoleSystem, leadingBlocks))
				if cachedHistory {
					tokens += llm.EstimateMessageTokens(out[len(out)-1])
				}
			}
			out = append(out, visible[lead:]...)
		} else {
			out = append(out, visible...)
			if pre := l.thinToolsPreamble(); pre != "" {
				out = append(out, llm.Message{Role: llm.RoleSystem, Content: pre, Origin: &llm.ContextOrigin{Source: llm.SourceInstructions}})
				if cachedHistory {
					tokens += llm.EstimateMessageTokens(out[len(out)-1])
				}
			}
		}
		out = append(out, l.trailingContextMessage())
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
	blocks := []llm.ContextBlock{{Source: llm.SourceInstructions, Text: system}, {Source: llm.SourceMemory, Text: l.briefing}, {Source: llm.SourceInstructions, Text: l.userInstructions}}
	out := []llm.Message{llm.MessageWithContextBlocks(llm.RoleSystem, blocks)}

	window, _, nextStart := chatHistoryProjectionWithMediaCost(visible, l.chatWindowStart, false, true)
	l.chatWindowStart = nextStart
	out = append(out, window...)
	// Per-request freshness stamp at the very END, same pattern as the
	// coordinator route: the minute-granular stamp used to be baked into
	// the leading system prompt, rewriting the prompt front every minute
	// and killing the provider-side KV cache. The provider demote pass
	// renders this trailing system message in place as a
	// <system-reminder> user turn.
	out = append(out, l.trailingContextMessage())
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
	return llm.MessageWithContextBlocks(llm.RoleSystem, l.contextTailBlocks()).Content
}

func (l *Loop) contextTailBlocks() []llm.ContextBlock {
	blocks := append([]llm.ContextBlock(nil), l.liveContextBlocks...)
	if l.liveContext != "" {
		blocks = append(blocks, llm.ContextBlock{Source: llm.ContextUnknown, Text: l.liveContext})
	}
	if archive := l.historyRecallContext(); archive != "" {
		blocks = append(blocks, llm.ContextBlock{Source: llm.SourceRuntime, Text: archive})
	}
	blocks = append(blocks, llm.ContextBlock{Source: llm.SourceRuntime, Text: l.stampSection()})
	blocks = append(blocks, llm.ContextBlock{Source: llm.SourceRuntime, Text: l.completedOps.context()})
	blocks = append(blocks, llm.ContextBlock{Source: llm.SourceRuntime, Text: l.downloadExportContext})
	if l.ultraworkMode {
		// Preserve the pre-existing byte layout (this section supplies its own separator).
		last := len(blocks) - 1
		for last > 0 && blocks[last].Text == "" {
			last--
		}
		blocks[last].Text += ultrawork.SystemPromptSection()
		blocks = append(blocks, llm.ContextBlock{Source: llm.SourceRuntime, Text: l.ultraworkReminder})
	}
	blocks = append(blocks, llm.ContextBlock{Source: llm.SourceInstructions, Text: l.requestedToolContext()}, llm.ContextBlock{Source: llm.SourceInstructions, Text: l.implementationHintForRun})
	return blocks
}

func (l *Loop) trailingContextMessage() llm.Message {
	blocks := l.contextTailBlocks()
	if l.finalReplyOnly {
		blocks = append(blocks, llm.ContextBlock{Source: llm.SourceInstructions, Text: "[final reply only] The requested Word operation succeeded. Do not call or describe another tool. Briefly tell the user that the document is ready, include its path, and stop."})
	} else if l.keepThinking && l.lastThinking != "" {
		blocks = append(blocks, llm.ContextBlock{Source: llm.SourceModel, Text: "[Retained reasoning from your previous turn — treat it as your own chain of thought and continue from it; do not reveal it to the user:]\n" + l.lastThinking})
	}
	return llm.MessageWithContextBlocks(llm.RoleSystem, blocks)
}
