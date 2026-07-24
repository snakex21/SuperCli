package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"supercli/internal/llm"
	"supercli/internal/system/stats"
)

func (l *Loop) completeOnce(ctx context.Context, toolDefs []llm.ToolDef, out chan<- Event) (string, []llm.ToolCall, *llm.Usage, error) {
	// context_prepare part 2: provider message assembly (visible
	// view, thin preamble placement, freshness stamp).
	msgStart := time.Now()
	msgs := l.providerMessages()
	// Calibrate against the same local projection used by the NEXT compaction
	// check. Chat/advisor routes intentionally send a smaller history window;
	// comparing that wire-only estimate with the full logical estimate on the
	// next turn would manufacture a large delta that was never appended.
	requestEstimate := estimateRequestTokens(msgs, toolDefs)
	l.recordWallPhase(stats.PhaseContextPrepare, time.Since(msgStart))

	// request_encode: provider.Complete up to the stream handoff —
	// request serialization plus whatever the provider does before
	// returning its delta channel.
	encStart := time.Now()
	stream, err := l.provider.Complete(ctx, msgs, toolDefs)
	l.recordWallPhase(stats.PhaseRequestEncode, time.Since(encStart))
	if err != nil {
		return "", nil, nil, fmt.Errorf("agent: provider.Complete: %w", err)
	}
	text, calls, usage, err := l.consume(ctx, stream, out)
	if err == nil && usage != nil {
		l.recordContextBaseline(requestEstimate, usage.Input)
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
	if l.route == RouteCoordinator {
		// Per-request freshness stamp: appended at the END so the stable
		// prompt prefix stays cacheable by the provider.
		visible := l.VisibleMessages()
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
				if text := messageDraftText(msg); text != "" {
					leading = append(leading, text)
				}
			}
			if l.hoistedPre != "" {
				leading = append(leading, l.hoistedPre)
			}
			if len(leading) > 0 {
				out = append(out, llm.Message{Role: llm.RoleSystem, Content: strings.Join(leading, "\n\n")})
			}
			out = append(out, visible[lead:]...)
		} else {
			out = append(out, visible...)
			if pre := l.thinToolsPreamble(); pre != "" {
				out = append(out, llm.Message{Role: llm.RoleSystem, Content: pre})
			}
		}
		out = append(out, llm.Message{Role: llm.RoleSystem, Content: l.stampSection()})
		return out
	}
	visible := l.VisibleMessages()
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

	// The current turn (everything from the last user message on) is sent
	// verbatim so tool_call/tool_result pairing stays intact when the model
	// uses tool_search or recall on this route.
	lastUser := -1
	for i := len(visible) - 1; i >= 0; i-- {
		if visible[i].Role == llm.RoleUser && !strings.Contains(visible[i].Content, "<task-notification>") {
			lastUser = i
			break
		}
	}
	// Conversational history before the current turn: a GROWING
	// (append-only) window, not a sliding one. A per-turn "last 8" tail
	// rewrote the prompt front every turn, so the provider-side KV
	// cache could never reuse more than the leading system prompt —
	// a construction-level cache killer on these light routes. Instead
	// the window start is sticky (l.chatWindowStart): below the token
	// threshold each turn strictly appends to the previous prompt (full
	// prefix cache hit); once the window outgrows the threshold, the
	// start jumps forward in ONE big leap, keeping only the last
	// chatWindowKeepMsgs messages — the re-eval is paid once per many
	// turns, not every turn ("cut rarely, in big chunks").
	//
	// Eligibility (user/assistant only, no task notifications, no tool
	// calls) is unchanged from the sliding tail: background agent work
	// must not leak into smalltalk. A current turn that used tools is
	// therefore trimmed from history on the NEXT turn — that single
	// divergence point costs one partial re-eval, which is fine (rare
	// on chat routes) and always safe (the server re-evals from the
	// divergence).
	end := len(visible)
	if lastUser >= 0 {
		end = lastUser
	}
	start := l.chatWindowStart
	if start > end {
		// The visible view shrank under us (compaction, /clear).
		// Restart the window; correctness never depends on it.
		start = 0
		l.chatWindowStart = 0
	}
	window := make([]llm.Message, 0, end-start)
	for i := start; i < end; i++ {
		if chatWindowEligible(visible[i]) {
			window = append(window, visible[i])
		}
	}
	if llm.EstimateTokens(window) > chatWindowMaxTokens {
		// One big jump: advance the sticky start so only the last
		// chatWindowKeepMsgs eligible messages stay in the window.
		kept := 0
		ns := end
		for i := end - 1; i >= start && kept < chatWindowKeepMsgs; i-- {
			if chatWindowEligible(visible[i]) {
				kept++
				ns = i
			}
		}
		l.chatWindowStart = ns
		window = window[len(window)-kept:]
	}
	out = append(out, window...)
	if lastUser >= 0 {
		for _, m := range visible[lastUser:] {
			if m.Role == llm.RoleSystem {
				continue
			}
			out = append(out, m)
		}
	}
	// Per-request freshness stamp at the very END, same pattern as the
	// coordinator route: the minute-granular stamp used to be baked into
	// the leading system prompt, rewriting the prompt front every minute
	// and killing the provider-side KV cache. The provider demote pass
	// renders this trailing system message in place as a
	// <system-reminder> user turn.
	out = append(out, llm.Message{Role: llm.RoleSystem, Content: l.stampSection()})
	return out
}
