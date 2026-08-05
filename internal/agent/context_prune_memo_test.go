package agent

import (
	"context"
	"strings"
	"testing"

	"supercli/internal/llm"
)

// TestPrune_RefusalIsMemoizedUntilContextGrows locks the memoization
// down BEHAVIOURALLY, without counting function calls: after a refusal
// the test makes the very same history prunable (protect drops to a
// single token) while leaving the estimate untouched. If the O(history)
// scan still ran, it would now find victims and prune — so "no prune"
// proves the scan was skipped. Growing the context then re-arms it.
func TestPrune_RefusalIsMemoizedUntilContextGrows(t *testing.T) {
	// Protect far more than the history holds: every tool result is in
	// the protected tail, nothing is reclaimable => refusal.
	l := pruneLoop(t, 6000, 1_000_000)
	bigToolTurn(l, "turn 1", 4, 4000)
	l.invalidateVisibleEstimate()

	if got := l.maybePruneToolResults(context.Background(), nil); got != 0 {
		t.Fatalf("setup: expected a refusal, pruned %d tokens", got)
	}
	est := l.EstimateNextRequestTokens()
	if l.pruneRefusedEst != est {
		t.Fatalf("refusal not memoized: pruneRefusedEst = %d, estimate = %d "+
			"(is the estimate even above the %.0f%% trigger?)",
			l.pruneRefusedEst, est, pruneTriggerFrac*100)
	}

	// Same estimate, but the config now makes everything prunable. A
	// re-scan would cut; the memo must keep us out of the scan.
	l.pruneProtect = 1
	if got := l.maybePruneToolResults(context.Background(), nil); got != 0 {
		t.Fatalf("re-scanned after an unchanged estimate (reclaimed %d) — "+
			"the refusal memo is not holding", got)
	}
	for _, m := range l.Messages {
		if strings.HasPrefix(m.Content, pruneMarkerPrefix) {
			t.Fatal("history was rewritten while the refusal memo was valid")
		}
	}

	// Real growth: the verdict may flip, so the scan must run again.
	bigToolTurn(l, "turn 2", 4, 4000)
	l.invalidateVisibleEstimate()
	grown := l.EstimateNextRequestTokens()
	if float64(grown) < float64(est)*(1+pruneRecheckGrowFrac) {
		t.Fatalf("fixture did not grow the estimate past the recheck threshold: %d -> %d", est, grown)
	}
	if got := l.maybePruneToolResults(context.Background(), nil); got <= 0 {
		t.Fatalf("no prune after the context grew past the recheck threshold "+
			"(estimate %d -> %d): the memo is sticky", est, grown)
	}
	if l.pruneRefusedEst != 0 {
		t.Errorf("memo survived an actual prune: pruneRefusedEst = %d, want 0", l.pruneRefusedEst)
	}
}

// TestPrune_MemoDoesNotBlockAShrinkingContext: after compaction the
// history composition changes even though the estimate falls. A
// smaller estimate must re-open the scan rather than reuse the memo.
func TestPrune_MemoDoesNotBlockAShrinkingContext(t *testing.T) {
	l := pruneLoop(t, 6000, 1_000_000)
	bigToolTurn(l, "turn 1", 6, 4000)
	l.invalidateVisibleEstimate()
	if got := l.maybePruneToolResults(context.Background(), nil); got != 0 {
		t.Fatalf("setup: expected a refusal, pruned %d tokens", got)
	}
	if l.pruneRefusedEst == 0 {
		t.Fatal("setup: refusal was not memoized")
	}

	// Shrink the history (as compaction would) and make it prunable.
	l.Messages = l.Messages[:len(l.Messages)-4]
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleAssistant, Content: "done"})
	l.invalidateVisibleEstimate()
	l.pruneProtect = 1
	if got := l.maybePruneToolResults(context.Background(), nil); got <= 0 {
		t.Fatalf("a shrunken, prunable history was skipped by the memo (reclaimed %d)", got)
	}
}
