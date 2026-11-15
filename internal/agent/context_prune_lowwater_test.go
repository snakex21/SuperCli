package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
)

// longSingleTurn is one user turn with n tool steps of ~size chars each and
// a current step (the last result) the model is reasoning over.
func longSingleTurn(l *Loop, n, size int) {
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleUser, Content: "process every file"})
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("c%d", i)
		l.Messages = append(l.Messages,
			llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "read_lines", Arguments: fmt.Sprintf(`{"path":"f%d.go"}`, i)}}},
			llm.Message{Role: llm.RoleTool, ToolCallID: id, Name: "read_lines", Content: strings.Repeat("x", size)},
		)
	}
}

func toolIndexes(l *Loop) []int {
	var out []int
	for i, m := range l.Messages {
		if m.Role == llm.RoleTool {
			out = append(out, i)
		}
	}
	return out
}

// TestPrune_LongTurnPrunesToLowWater: an accepted prune inside a long single
// turn continues into the protected tail until the request is under the low
// water mark, so the next cache-rewriting prune is further away. The current
// step and the newest earlier results stay verbatim.
func TestPrune_LongTurnPrunesToLowWater(t *testing.T) {
	const window = 10000
	l := pruneLoop(t, window, 5000)
	longSingleTurn(l, 16, 3000)
	before := l.EstimateNextRequestTokens()
	if before <= int(pruneTriggerFrac*window) {
		t.Fatalf("fixture below trigger: %d", before)
	}
	if l.maybePruneToolResults(context.Background(), nil) == 0 {
		t.Fatal("expected a prune")
	}
	after := l.EstimateNextRequestTokens()
	if lowWater := int(pruneLowWaterFrac * window); after > lowWater {
		t.Fatalf("prune stopped at %d, above the low-water mark %d", after, lowWater)
	}
	idx := toolIndexes(l)
	// Current step plus the pruneDeepenKeep newest earlier results.
	for _, i := range idx[len(idx)-1-pruneDeepenKeep:] {
		if strings.HasPrefix(l.Messages[i].Content, pruneMarkerPrefix) {
			t.Fatalf("result %d of the newest steps was pruned", i)
		}
	}
	if !strings.HasPrefix(l.Messages[idx[0]].Content, pruneMarkerPrefix) {
		t.Fatal("oldest result not pruned")
	}

	// The same history in a short turn keeps the protected tail exactly.
	short := pruneLoop(t, window, 5000)
	bigToolTurn(short, "old turn", 8, 4000)
	bigToolTurn(short, "last turn", 3, 4000)
	if short.maybePruneToolResults(context.Background(), nil) == 0 {
		t.Fatal("expected a prune in the short-turn fixture")
	}
	shortIdx := toolIndexes(short)
	for _, i := range shortIdx[len(shortIdx)-4:] {
		if strings.HasPrefix(short.Messages[i].Content, pruneMarkerPrefix) {
			t.Fatalf("short turn: protected result %d was pruned", i)
		}
	}
}

// TestPrune_LowWaterNeverStartsAPrune: below the 25% gain gate nothing is
// rewritten, however deep the protected tail is.
func TestPrune_LowWaterNeverStartsAPrune(t *testing.T) {
	const window = 10000
	l := pruneLoop(t, window, 100000) // everything protected: no regular victims
	longSingleTurn(l, 12, 4000)
	if got := l.maybePruneToolResults(context.Background(), nil); got != 0 {
		t.Fatalf("deepening started a prune on its own: reclaimed %d", got)
	}
	for _, i := range toolIndexes(l) {
		if strings.HasPrefix(l.Messages[i].Content, pruneMarkerPrefix) {
			t.Fatal("marker written")
		}
	}
}
