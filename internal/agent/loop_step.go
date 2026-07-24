package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"supercli/internal/llm"
	"supercli/internal/system/stats"
)

// stepResult controls the outer run() loop after one provider/tool cycle.
type stepResult int

const (
	stepContinue stepResult = iota // next step
	stepDone                       // DoneEvent already sent
	stepAbort                      // ErrorEvent already sent (or fatal)
)

// runStep executes one agent step: draft → context defense → complete →
// tools → reflection. Mutates totalUsage, reflectionProgress, limitProgress
// and *stepLimit in place.
func (l *Loop) runStep(
	ctx context.Context,
	step int,
	out chan<- Event,
	totalUsage *Usage,
	reflectionProgress *adaptiveReflectionProgress,
	limitProgress *stepLimitProgress,
	discoveryProg *discoveryProgress,
	stepLimit *int,
	hardStepLimit int,
) stepResult {
	if err := ctx.Err(); err != nil {
		out <- ErrorEvent{Err: err, Usage: *totalUsage, Steps: step}
		return stepAbort
	}
	l.retryDirtyProjection(ctx)

	// Phase telemetry: one stats turn per step. All timings are
	// whole-phase time.Since measurements — nothing is measured
	// per-delta, so the streaming hot path stays allocation-free.
	stepStart := time.Now()
	l.statsStartStep(step + 1)

	// F11 draft step: ask the bridge whether
	// this step should get a draft, and if so,
	// call the draft model and inject the plan
	// as a system message. We do this BEFORE
	// the verifier call so the verifier sees
	// the draft on its first turn.
	//
	// upcomingTools is the verifier's planned
	// tool calls from the PREVIOUS step
	// (zero on step 0). The policy uses it to
	// decide CriticalOnly drafts.
	ok, _ := l.draftPolicy.ShouldDraft(step, l.upcomingTools)
	if l.draftBridge != nil && ok {
		l.invokeDraft(ctx, step, out)
	}

	// Context defense before the provider call, cheap first:
	// (1) prune old tool results in place (zero model calls,
	// prune.go), (2) only if the estimate still enters the reserved
	// generation space, fall back to summary compaction.
	prepStart := time.Now()
	l.maybePruneToolResults(ctx, out)
	auxBefore := l.stepAuxWall
	l.maybeAutoCompact(ctx, out, "")

	// Build tool definitions from visible tools. Non-coordinator routes
	// get only the minimal chatRouteTools set (tool_search + recall) so
	// the model can pull in more when needed; the full tool list is the
	// actual token cost the router avoids.
	//
	// forceReplyWithoutTools: one shot with zero tool defs so a local
	// model cannot ignore a "do not call tools" system line.
	var toolDefs []llm.ToolDef
	forcedNoTools := l.forceReplyWithoutTools
	if forcedNoTools {
		toolDefs = nil
		l.forceReplyWithoutTools = false
	} else {
		toolDefs = l.buildToolDefs()
	}
	// context_prepare part 1: prune/compact/tool defs. Part 2
	// (provider message assembly) is added inside completeOnce.
	// The auto-compact SUMMARY MODEL CALL is excluded — it is
	// booked as its own model:compact phase (recordAuxWall in
	// maybeAutoCompact), so context_prepare stays pure CLI time.
	prep := time.Since(prepStart) - (l.stepAuxWall - auxBefore)
	if prep < 0 {
		prep = 0
	}
	l.recordWallPhase(stats.PhaseContextPrepare, prep)

	text, toolCalls, usage, err := l.completeOnce(ctx, toolDefs, out)
	if err != nil && l.handleContextOverflow(ctx, err, out) {
		// Wave 4: provider rejected the context size.
		// The learned limit was persisted and the
		// conversation compacted; retry once. The retry's phase
		// timings accumulate onto this step's (documented in
		// stats.Turn.Phases). Keep the same toolDefs (incl. empty
		// force-reply set) so overflow retry does not re-arm tools.
		text, toolCalls, usage, err = l.completeOnce(ctx, toolDefs, out)
	}
	// Even if the model hallucinates tool_calls without defs, drop them
	// on a forced-reply turn so we never execute more discovery.
	if forcedNoTools && len(toolCalls) > 0 {
		toolCalls = nil
		if strings.TrimSpace(text) == "" {
			text = "(forced reply: tools disabled after discovery budget)"
		}
	}
	if err != nil {
		l.statsEndStep(stepStart)
		out <- ErrorEvent{Err: err, Usage: *totalUsage, Steps: step + 1}
		return stepAbort
	}
	// Resolve the schema-stable invoke_tool dispatcher before the
	// assistant/tool-result pair enters history. Valid direct calls become
	// ordinary target calls, so verification, error attribution and
	// telemetry all retain the real tool name.
	toolCalls = l.resolveInvokeToolCalls(toolCalls)
	if usage != nil {
		totalUsage.Input += usage.Input
		totalUsage.Output += usage.Output
		totalUsage.Total += usage.Total
		totalUsage.Cached += usage.CachedInput
		totalUsage.Reasoning += usage.Reasoning
		l.sessUsageMu.Lock()
		l.sessUsage.Input += usage.Input
		l.sessUsage.Output += usage.Output
		l.sessUsage.Total += usage.Total
		l.sessUsage.Cached += usage.CachedInput
		l.sessUsage.Reasoning += usage.Reasoning
		// Last-turn snapshot for the status-line badges.
		l.lastTurnPrompt = usage.Input
		l.lastTurnCached = usage.CachedInput
		l.lastTurnOutput = usage.Output
		l.lastTurnReasoning = usage.Reasoning
		l.lastTurnSet = true
		l.sessUsageMu.Unlock()
		// Provider-reported prompt/completion tokens for the
		// phase telemetry (llama.cpp and the cloud backends
		// report usage on the final delta).
		if l.stats != nil {
			l.stats.RecordTokens(usage.Input, usage.Output)
			l.stats.RecordModel(l.modelID)
		}
		// Report per-turn usage to the writer (if any).
		// Failures feed the persistence-health tracker
		// (sticky first error + /status) but never abort
		// the run.
		if l.writer != nil {
			if err := l.writer.UpdateUsage(usage.Input, usage.Output); err != nil {
				l.persistUsageFailure(err)
			}
		}
		// F7: record to credit tracker. A budget
		// cap is a hard stop, but we still emit
		// the partial usage for the turn.
		if l.creditTracker != nil {
			if err := l.creditTracker.Record(ctx, int64(usage.Input), int64(usage.Output), l.modelID); err != nil {
				l.statsEndStep(stepStart)
				out <- ErrorEvent{Err: err, Usage: *totalUsage, Steps: step + 1}
				return stepAbort
			}
		}
		// F11: charge the draft call's tokens
		// against the same tracker. Draft spend
		// shares the user's F7 budget (per D2
		// decision). The draft provider's
		// usage is captured at the end of
		// invokeDraft.
		if l.draftSavings != nil && l.lastDraftTokens > 0 {
			if err := l.recordDraftUsage(ctx); err != nil {
				l.statsEndStep(stepStart)
				out <- ErrorEvent{Err: err, Usage: *totalUsage, Steps: step + 1}
				return stepAbort
			}
		}
	}

	// F11: compute the savings / override signal
	// by comparing the verifier's first text to
	// the draft we injected. Emits DraftUsedEvent
	// with the decision and feeds savings into
	// stats + override sink. Must run AFTER
	// consume() so we have the verifier's text
	// and token counts.
	verifyTokens := 0
	if usage != nil {
		verifyTokens = usage.Output
	}
	if l.draftBridge != nil && l.lastDraftText != "" {
		l.recordDraftOutcome(step, text, verifyTokens, out)
	}

	// Build the assistant message and append it to history.
	// Two views: the in-memory history that drives the NEXT
	// provider request keeps only the final answer (reasoning
	// blocks stripped, per Qwen/DeepSeek convention — prior-turn
	// chain-of-thought is not context and re-sending it bloats
	// every subsequent turn). The persisted copy keeps the full
	// text with <thinking> so the UI can replay it. Stripping here
	// (at append time), not in-flight, keeps the cacheable prefix
	// deterministic: the history bytes never change afterwards.
	assistant := llm.Message{Role: llm.RoleAssistant}
	if text != "" {
		assistant.Parts = append(assistant.Parts, llm.ContentPart{Type: llm.PartTypeText, Text: text})
	}
	for _, tc := range toolCalls {
		assistant.ToolCalls = append(assistant.ToolCalls, llm.ToolCall{
			ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments,
		})
	}
	l.persist(ctx, assistant)
	l.Messages = append(l.Messages, stripThinkingFromMessage(assistant))

	// F14: budget-based eviction. After every step we
	// look at the visible token estimate; if it
	// exceeds 80% of the F7 per-session cap, the
	// oldest non-system messages get hidden from the
	// next provider call. Persisted and searchable
	// copies stay intact; only the provider's view
	// is trimmed.
	l.EvictForBudget(ctx, out)

	// F11: capture the names of the tool calls the
	// verifier is about to make so the NEXT
	// iteration's draft policy can decide
	// CriticalOnly. Empty when the model emitted
	// no tool calls (text-only step).
	l.upcomingTools = nil
	for _, tc := range toolCalls {
		l.upcomingTools = append(l.upcomingTools, tc.Name)
	}

	// Tool-calls-per-step is THE parallelism-worthiness metric:
	// count the raw batch size (duplicates included) plus the
	// unique tool names for the per-turn report.
	if l.stats != nil && len(toolCalls) > 0 {
		l.stats.RecordToolCalls(len(toolCalls))
		l.stats.RecordTools(l.upcomingTools)
	}

	if len(toolCalls) == 0 {
		// A user may have typed while this provider call was running. Treat
		// that as the next user turn instead of completing the Run and making
		// them wait/re-submit. Draining here is a safe history boundary: the
		// assistant message above is already complete and persisted.
		if l.drainInterjections(ctx) > 0 {
			l.statsEndStep(stepStart)
			return stepContinue
		}
		// Empty final reply after tools is a product failure: the user asked
		// a question, tools ran, and nothing was said. Force one more turn
		// with a system instruction (no tool_search required). Once per Run.
		if strings.TrimSpace(text) == "" && !l.emptyReplyNudgeUsed && messagesHaveRecentToolResult(l.Messages) {
			l.emptyReplyNudgeUsed = true
			sys := llm.Message{
				Role: llm.RoleSystem,
				Content: "[reply required] You used tools and returned an empty message. " +
					"Answer the user now in their language with a clear, useful summary of the tool results. " +
					"Do not call tools unless the answer is still impossible. Never end with an empty reply.",
			}
			l.Messages = append(l.Messages, sys)
			l.persist(ctx, sys)
			out <- NoticeEvent{Text: "empty reply after tools: forcing a user-facing answer"}
			l.statsEndStep(stepStart)
			return stepContinue
		}
		// F9 Sisyphus: when ultrawork is on AND
		// the active /goal still has unfinished
		// tasks, re-prompt the model instead of
		// emitting DoneEvent. The reminder becomes
		// a system message in the conversation so
		// the next iteration of the loop sees it.
		if l.ultraworkMode && l.ultraworkSisyphus != nil {
			if should, msg := l.ultraworkSisyphus.ShouldContinue(ctx); should {
				sys := llm.Message{Role: llm.RoleSystem, Content: msg}
				l.Messages = append(l.Messages, sys)
				l.persist(ctx, sys)
				// SisyphusEvent uses a separate Hit
				// counter (1-indexed) so the TUI can
				// label it "Sisyphus #1/3" without
				// confusing it with the F5 reflection
				// step.
				out <- SisyphusEvent{
					Step: step + 1,
					Hit:  sisyphusHitFromMessage(msg),
					Text: msg,
				}
				l.statsEndStep(stepStart)
				return stepContinue
			}
		}
		l.statsEndStep(stepStart)
		out <- DoneEvent{Usage: *totalUsage, Steps: step + 1}
		return stepDone
	}

	toolStart := time.Now()
	toolsOK, toolFailures := l.invokeToolCalls(ctx, toolCalls, out)
	l.recordWallPhase(stats.PhaseToolExecution, time.Since(toolStart))
	if !toolsOK {
		l.statsEndStep(stepStart)
		return stepAbort
	}
	progressing := limitProgress.observe(toolCalls, toolFailures)
	if discoveryProg != nil {
		switch discoveryProg.observe(toolCalls, toolFailures) {
		case discoveryNudge:
			nudge := llm.Message{
				Role: llm.RoleSystem,
				Content: "[strategy] Too many tool calls without real progress (successful edit/run). " +
					"Stop expanding searches and re-reads. Use what you already have: patch_file/create_file, " +
					"or give a clear blocked answer to the user. Do not call tool_search for listing, reading, or editing files.",
			}
			l.Messages = append(l.Messages, nudge)
			l.persist(ctx, nudge)
			out <- NoticeEvent{Text: "discovery-without-progress: strategy nudge"}
			discoveryProg.noteNudge()
		case discoveryForceReply:
			// Hard limit: next Complete gets toolDefs=nil (physical), not only
			// a prompt. emptyReplyNudgeUsed stays false so empty-text still works.
			force := llm.Message{
				Role: llm.RoleSystem,
				Content: "[reply required] Tool budget for discovery is exhausted without a successful mutation or execution. " +
					"Answer the user now in their language with what you know, what failed, and what is blocked. " +
					"Tools are disabled for this turn.",
			}
			l.Messages = append(l.Messages, force)
			l.persist(ctx, force)
			l.forceReplyWithoutTools = true
			out <- NoticeEvent{Text: "discovery-without-progress: forcing user-facing answer (tools off)"}
			discoveryProg.reset()
		}
	}
	// Tool results have all been appended in deterministic call order. This
	// is the other safe drain point for mid-turn user messages.
	l.drainInterjections(ctx)

	// F5.a: default to signal-driven reflection. A healthy run pays no
	// auxiliary inference merely because it crossed an arbitrary step
	// number. Explicit fixed intervals remain available to embedders and
	// users who set reflect_every=N.
	reason := ""
	if l.adaptiveReflect {
		reason = reflectionProgress.observe(step+1, *stepLimit, toolCalls, toolFailures)
	} else if l.reflectEvery > 0 && (step+1)%l.reflectEvery == 0 {
		reason = "fixed_interval"
	}
	if reason != "" {
		l.runReflection(ctx, step+1, reason, out)
		reflectionProgress.reset()
	}

	if step+1 == *stepLimit && *stepLimit < hardStepLimit && progressing {
		previous := *stepLimit
		*stepLimit = min(*stepLimit+5, hardStepLimit)
		out <- NoticeEvent{Text: fmt.Sprintf(
			"step budget extended from %d to %d because %d/%d tool calls succeeded and work is still progressing",
			previous, *stepLimit, len(toolCalls)-toolFailures, len(toolCalls),
		)}
	}

	l.statsEndStep(stepStart)
	return stepContinue
}
