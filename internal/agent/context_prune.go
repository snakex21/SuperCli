package agent

import (
	"context"
	"fmt"
	"strings"

	"supercli/internal/llm"
)

// Tool-result pruning: the first line of context defense, before the
// summary fallback (window.go) and with ZERO extra model calls.
//
// When the visible token estimate crosses pruneTriggerFrac of the
// model's window, old RoleTool results are replaced in place with a
// short marker. The paired assistant message (tool name + arguments)
// is never touched, so the model can re-run the tool if it really
// needs the data again; the original result also stays in the F13
// session store (prune mutates only the in-memory copy, persisted
// rows are not rewritten).
//
// KV-cache rules the implementation must keep:
//   - prune RARELY and in ONE BIG BATCH: every prune invalidates the
//     server-side prompt cache from the first edited message, so it
//     only runs when at least pruneMinGainFrac of the current
//     estimate can be reclaimed (one re-eval, big payoff);
//   - between prunes the history stays append-only: victims are
//     rewritten exactly once (markers are never rewritten again);
//   - only RoleTool results are touched — never user text, assistant
//     text or tool-call arguments;
//   - the freshest pruneProtect tokens of tool results are always
//     kept, and the current step's results (everything after the last
//     tool-calling assistant message) are untouchable on top of that.
//     With the 8k default a normal TUI turn fits entirely inside the
//     protected tail, so the last user turn survives whole; a single
//     giant agentic turn (many tool steps in one Run) can still shed
//     its oldest results instead of being forced straight into
//     summary compaction.
const (
	// pruneTriggerFrac: don't even look at the history below this
	// fraction of the context window (summary compaction is the later fallback).
	pruneTriggerFrac = 0.6
	// pruneMinGainFrac: minimum reclaimable share of the current
	// estimate for a prune to be worth one cache re-eval.
	pruneMinGainFrac      = 0.25
	minPruneProtectTokens = 2048
	maxPruneProtectTokens = 65536
	// pruneRecheckGrowFrac: after a refusal ("nothing worth
	// reclaiming yet") the full history scan is not repeated until
	// the request estimate has grown by this fraction. Above the
	// 60% trigger the scan is O(history) and ran on EVERY step,
	// re-deciding the same "no" over and over on a long session.
	// The verdict can only flip when the context actually grows, so
	// growth is the only thing worth waking up for.
	pruneRecheckGrowFrac = 0.1
)

// defaultPruneProtectTokens scales the verbatim tool-result tail with the
// model window. The former fixed 8192-token tail consumed half of a 16k model
// yet protected less than one percent of a million-token session.
func defaultPruneProtectTokens(window int) int {
	protect := window / 16
	if protect < minPruneProtectTokens {
		protect = minPruneProtectTokens
	}
	if protect > maxPruneProtectTokens {
		protect = maxPruneProtectTokens
	}
	if window > 0 && protect >= window {
		protect = window / 4
		if protect < 1 {
			protect = 1
		}
	}
	return protect
}

// pruneMarkerPrefix identifies already-pruned results so a later pass
// never rewrites them again (append-only between prunes).
const pruneMarkerPrefix = "[tool result pruned"

// pruneMarker is the replacement body. The tool call itself (name +
// arguments) sits untouched on the preceding assistant message.
func pruneMarker(tool string) string {
	if tool == "" {
		tool = "the tool"
	}
	return fmt.Sprintf("%s to save context — re-run %s with the same arguments if needed]", pruneMarkerPrefix, tool)
}

// prunable reports whether l.Messages[i] is a tool result that MAY be
// pruned (not hidden, not already a marker, big enough that the
// marker is actually smaller).
func (l *Loop) prunable(i int) bool {
	m := l.Messages[i]
	if m.Role != llm.RoleTool {
		return false
	}
	// Applied skill guidance is an instruction, not disposable command
	// output. Keep it until normal conversation compaction summarizes it.
	if m.Name == "apply_skill" {
		return false
	}
	if i < len(l.hidden) && l.hidden[i] {
		return false
	}
	if strings.HasPrefix(m.Content, pruneMarkerPrefix) {
		return false
	}
	marker := llm.EstimateMessageTokens(llm.Message{Role: llm.RoleTool, Content: pruneMarker(m.Name)})
	return llm.EstimateMessageTokens(m) > 2*marker
}

// maybePruneToolResults runs before every provider call, ahead of
// maybeAutoCompact. It returns the estimated tokens reclaimed (0 =
// no prune happened).
func (l *Loop) maybePruneToolResults(ctx context.Context, out chan<- Event) int {
	if l.pruneProtect < 0 {
		return 0 // disabled via config
	}
	w := l.window()
	hardThreshold := autoCompactThreshold(w)
	prefillBudget, adaptive := l.adaptivePrefillBudget(hardThreshold)
	trigger := int(pruneTriggerFrac * float64(w))
	triggerSource := "context-window"
	workingWindow := w
	if adaptive && prefillBudget < trigger {
		trigger = prefillBudget
		triggerSource = "prefill-profile"
		workingWindow = prefillBudget
	}
	protect := l.pruneProtect
	if protect == 0 {
		protect = defaultPruneProtectTokens(workingWindow)
	}
	est := l.EstimateNextRequestTokens()
	if est <= trigger {
		return 0
	}
	// Memoized refusal: the previous scan already decided that too
	// little is reclaimable, and that verdict cannot change while the
	// context stays the same size. Skip the whole O(history) walk
	// until the estimate has grown by pruneRecheckGrowFrac. A SHRINK
	// (compaction, unhide) re-scans, since the composition changed.
	if l.pruneRefusedEst > 0 && est >= l.pruneRefusedEst &&
		float64(est) < float64(l.pruneRefusedEst)*(1+pruneRecheckGrowFrac) {
		return 0
	}
	historyEst := l.EstimateVisibleTokens()

	// The current step's results — everything after the last
	// assistant message that carries tool calls — are untouchable:
	// the model is actively reasoning over them.
	lastStep := -1
	for i := len(l.Messages) - 1; i >= 0; i-- {
		if l.Messages[i].Role == llm.RoleAssistant && len(l.Messages[i].ToolCalls) > 0 {
			lastStep = i
			break
		}
	}

	// Walk tool results newest-first: the freshest `protect` tokens
	// stay, everything older is a victim.
	acc := 0
	reclaimable := 0
	var victims []int
	for i := len(l.Messages) - 1; i >= 0; i-- {
		m := l.Messages[i]
		if m.Role != llm.RoleTool {
			continue
		}
		if i < len(l.hidden) && l.hidden[i] {
			continue // not in the provider's view, costs nothing
		}
		t := llm.EstimateMessageTokens(m)
		if i > lastStep {
			acc += t // current step: protected, but budget-counted
			continue
		}
		if acc < protect {
			acc += t
			continue
		}
		if !l.prunable(i) {
			continue
		}
		marker := llm.EstimateMessageTokens(llm.Message{Role: llm.RoleTool, Content: pruneMarker(m.Name)})
		victims = append(victims, i)
		reclaimable += t - marker
	}

	minGain := int(pruneMinGainFrac * float64(historyEst))
	if adaptive {
		// A measured prefill overage is worth reclaiming even when it is
		// smaller than the generic 25% cache-rewrite gate.
		if overage := est - prefillBudget; overage > 0 && overage < minGain {
			minGain = overage
		}
	}
	if reclaimable < minGain {
		l.pruneRefusedEst = est // remember: don't re-scan until it grows
		return 0                // not worth invalidating the KV cache
	}
	// A real prune rewrites history; the memo is meaningless now.
	l.pruneRefusedEst = 0

	for _, i := range victims {
		m := &l.Messages[i]
		m.Content = pruneMarker(m.Name)
		m.Parts = nil
	}
	l.invalidateVisibleEstimate()
	l.persistProjection(context.Background())

	ev := ToolResultsPrunedEvent{
		Pruned:          len(victims),
		Reclaimed:       reclaimable,
		Estimated:       est,
		Window:          w,
		Threshold:       trigger,
		ThresholdSource: triggerSource,
	}
	if out != nil {
		select {
		case out <- ev:
		case <-ctx.Done():
		}
	}
	return reclaimable
}
