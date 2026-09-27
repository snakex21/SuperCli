package agent

import (
	"context"
	"fmt"
	"strings"

	"supercli/internal/llm"
	"supercli/internal/tools/core"
)

// Tool-result pruning: the first line of context defense, before the
// summary fallback (window.go) and with ZERO extra model calls.
//
// When the visible token estimate crosses pruneTriggerFrac of the
// model's window, old RoleTool results are replaced in place with a
// short marker. The paired assistant message (tool name + arguments)
// is never touched. The marker preserves command exit status when available;
// it does not suggest repeating completed work. The original result stays in the F13
// session store (prune mutates only the in-memory copy, persisted
// rows are not rewritten). Inline results without an existing output handle
// are retained in one bounded archive only when a prune is accepted.
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
func pruneMarker(m llm.Message) string {
	name := m.Name
	if name == "" {
		name = "tool"
	}
	status := ""
	handle := core.StoredOutputHandle(m.Content)
	if m.Name == "ctx_execute" {
		if exit, ok := commandExitForPrune(m.Content, handle); ok {
			status = fmt.Sprintf(", exit_code=%d", exit)
		}
	} else if m.Name == "read_many" {
		status = readManyStatusForPrune(m.Content, handle)
	} else if m.Name == "process_session" {
		status = processStatusForPrune(m.Content, handle)
	} else if m.Name == "task" || m.Name == "send_message" {
		status = workerStatusForPrune(m.Content, handle)
	}
	if handle != "" {
		return fmt.Sprintf("%s: %s%s; read_output {\"handle\":%q}]", pruneMarkerPrefix, name, status, handle)
	}
	return fmt.Sprintf("%s: %s%s; details omitted]", pruneMarkerPrefix, name, status)
}

// prunable reports whether l.Messages[i] is a tool result that MAY be
// pruned (not hidden, not already a marker, big enough that the
// marker is actually smaller).
func (l *Loop) prunable(i int) bool {
	_, _, ok := l.pruneCandidate(i, llm.EstimateMessageTokens(l.Messages[i]))
	return ok
}

// Prepare each candidate marker once: command/process metadata may require
// decoding the original result. Reuse it for the gain calculation and rewrite.
func (l *Loop) pruneCandidate(i, resultTokens int) (string, int, bool) {
	m := l.Messages[i]
	if m.Role != llm.RoleTool || (i < len(l.hidden) && l.hidden[i]) || strings.HasPrefix(m.Content, pruneMarkerPrefix) {
		return "", 0, false
	}
	m = l.pruneSourceMessage(i)
	// Applied skill guidance is an instruction, including a late dispatch.
	if m.Name == "apply_skill" {
		return "", 0, false
	}
	marker := pruneMarker(m)
	markerTokens := llm.EstimateMessageTokens(llm.Message{Role: llm.RoleTool, Content: marker})
	return marker, markerTokens, resultTokens > 2*markerTokens
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
	history := l.pruningHistory()
	projected := history.requestMessages
	historyEst := llm.EstimateTokens(projected)

	// The current step's results — everything after the last
	// assistant message that carries tool calls — are untouchable:
	// the model is actively reasoning over them.
	lastStep := -1
	for i := len(projected) - 1; i >= 0; i-- {
		if projected[i].Role == llm.RoleAssistant && len(projected[i].ToolCalls) > 0 {
			lastStep = i
			break
		}
	}

	// Walk tool results newest-first: the freshest `protect` tokens
	// stay, everything older is a victim.
	acc := 0
	reclaimable := 0
	var victims []pruneVictim
	var archive pruneArchive
	for viewIndex := len(projected) - 1; viewIndex >= 0; viewIndex-- {
		m := projected[viewIndex]
		if m.Role != llm.RoleTool {
			continue
		}
		i := history.requestOriginalIndex(viewIndex)
		t := llm.EstimateMessageTokens(m)
		if viewIndex > lastStep {
			acc += t // current step: protected, but budget-counted
			continue
		}
		if acc < protect {
			acc += t
			continue
		}
		marker, markerTokens, ok := l.pruneCandidate(i, t)
		if !ok {
			continue
		}
		victim := pruneVictim{index: i, marker: marker}
		if l.registry != nil {
			markerTokens, ok = archive.plan(&victim, m.Content, t, markerTokens)
			if !ok {
				continue
			}
		}
		victims = append(victims, victim)
		reclaimable += t - markerTokens
	}

	minGain := int(pruneMinGainFrac * float64(historyEst))
	if adaptive {
		// A measured prefill overage is worth reclaiming even when it is
		// smaller than the generic 25% cache-rewrite gate.
		if overage := est - prefillBudget; overage > 0 && overage < minGain {
			minGain = overage
		}
	}
	if len(victims) == 0 || reclaimable < minGain {
		l.pruneRefusedEst = est // remember: don't re-scan until it grows
		return 0                // not worth invalidating the KV cache
	}
	// Shorter recent results can free room for other archived results to reenter
	// the request. Price that complete projection before paying for retention or
	// rewriting any cacheable prefix; per-result subtraction can overstate gain.
	reclaimable = l.projectedPruneGain(history, victims, pruneArchiveHandle)
	if reclaimable <= 0 || reclaimable < minGain {
		l.pruneRefusedEst = est
		return 0
	}
	handle := archive.save(ctx, l, victims)
	if handle == "" && archive.bytes > 0 {
		// A failed save uses shorter fallback markers, which can change the
		// recent-evidence selection again. Reject insufficient savings as above.
		reclaimable = l.projectedPruneGain(history, victims, "")
		if reclaimable <= 0 || reclaimable < minGain {
			l.pruneRefusedEst = est
			return 0
		}
	}
	// A real prune rewrites history; the memo is meaningless now.
	l.pruneRefusedEst = 0
	for _, victim := range victims {
		m := &l.Messages[victim.index]
		m.Content = victim.archiveMarker(handle)
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
