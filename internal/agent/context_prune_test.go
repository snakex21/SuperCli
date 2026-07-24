package agent

import (
	"context"
	"strings"
	"testing"

	"supercli/internal/llm"
)

// pruneLoop builds a loop with a fixed window and a small protected
// tail so tests can trip the prune with modest message sizes.
func pruneLoop(t *testing.T, window, protect int) *Loop {
	t.Helper()
	echo, _ := llm.NewEcho("test")
	return &Loop{
		provider:     echo,
		modelID:      "test",
		route:        RouteCoordinator,
		windowFor:    func(string) int { return window },
		pruneProtect: protect,
	}
}

func TestDefaultPruneProtectTokensScalesWithWindow(t *testing.T) {
	cases := map[int]int{
		16_384:    2048,
		100_000:   6250,
		1_050_000: 65_536,
	}
	for window, want := range cases {
		if got := defaultPruneProtectTokens(window); got != want {
			t.Errorf("defaultPruneProtectTokens(%d) = %d, want %d", window, got, want)
		}
	}
}

// bigToolTurn appends one user turn with n tool results of ~size
// chars each to l.Messages.
func bigToolTurn(l *Loop, user string, n, size int) {
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleUser, Content: user})
	for i := 0; i < n; i++ {
		l.Messages = append(l.Messages,
			llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c", Name: "read_lines", Arguments: `{"path":"x.go"}`}}},
			llm.Message{Role: llm.RoleTool, ToolCallID: "c", Name: "read_lines", Content: strings.Repeat("x", size)},
		)
	}
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleAssistant, Content: "done"})
}

func TestPrune_BelowTrigger_NoOp(t *testing.T) {
	l := pruneLoop(t, 100000, 100) // huge window: estimate never crosses 60%
	bigToolTurn(l, "turn 1", 4, 4000)
	bigToolTurn(l, "turn 2", 1, 100)
	if got := l.maybePruneToolResults(context.Background(), nil); got != 0 {
		t.Fatalf("pruned below trigger threshold: reclaimed %d", got)
	}
	for _, m := range l.Messages {
		if strings.HasPrefix(m.Content, pruneMarkerPrefix) {
			t.Fatal("marker written below trigger threshold")
		}
	}
}

func TestPrune_AppliedSkillGuidanceIsProtected(t *testing.T) {
	l := pruneLoop(t, 1000, 1)
	l.Messages = []llm.Message{{
		Role: llm.RoleTool, Name: "apply_skill",
		Content: strings.Repeat("important skill guidance ", 1000),
	}}
	if l.prunable(0) {
		t.Fatal("apply_skill guidance must not be treated as disposable tool output")
	}
}

// TestPrune_ReclaimsOldKeepsTailAndLastTurn: old tool results become
// markers; the protected token tail — sized here to cover the whole
// last turn plus the freshest old result — stays verbatim.
func TestPrune_ReclaimsOldKeepsTailAndLastTurn(t *testing.T) {
	l := pruneLoop(t, 10000, 5000)
	bigToolTurn(l, "old turn", 6, 6000)  // ~12k estimated tokens of tool results
	bigToolTurn(l, "last turn", 2, 6000) // ~4k: fits inside the protected tail
	estBefore := l.EstimateVisibleTokens()

	out := make(chan Event, 1)
	reclaimed := l.maybePruneToolResults(context.Background(), out)
	if reclaimed == 0 {
		t.Fatal("expected a prune")
	}
	estAfter := l.EstimateVisibleTokens()
	if estAfter >= estBefore {
		t.Fatalf("estimate did not drop: %d -> %d", estBefore, estAfter)
	}
	if diff := estBefore - estAfter; diff != reclaimed {
		t.Errorf("reported reclaim %d != actual estimate drop %d", reclaimed, diff)
	}

	// The last turn's tool results are all verbatim.
	lastUser := -1
	for i, m := range l.Messages {
		if m.Role == llm.RoleUser {
			lastUser = i
		}
	}
	for i := lastUser; i < len(l.Messages); i++ {
		if strings.HasPrefix(l.Messages[i].Content, pruneMarkerPrefix) {
			t.Errorf("last turn pruned at index %d", i)
		}
	}
	// The protected tail extends past the last turn into the old
	// turn: the newest old-turn result must be intact.
	var oldToolIdx []int
	for i := 0; i < lastUser; i++ {
		if l.Messages[i].Role == llm.RoleTool {
			oldToolIdx = append(oldToolIdx, i)
		}
	}
	newestOld := oldToolIdx[len(oldToolIdx)-1]
	if strings.HasPrefix(l.Messages[newestOld].Content, pruneMarkerPrefix) {
		t.Error("protected tail (freshest old tool result) was pruned")
	}
	if !strings.HasPrefix(l.Messages[oldToolIdx[0]].Content, pruneMarkerPrefix) {
		t.Error("oldest tool result was not pruned")
	}

	// Marker keeps the tool name; the paired assistant call keeps
	// name + arguments untouched.
	if !strings.Contains(l.Messages[oldToolIdx[0]].Content, "read_lines") {
		t.Errorf("marker lost the tool name: %q", l.Messages[oldToolIdx[0]].Content)
	}
	call := l.Messages[oldToolIdx[0]-1]
	if len(call.ToolCalls) != 1 || call.ToolCalls[0].Name != "read_lines" || !strings.Contains(call.ToolCalls[0].Arguments, "x.go") {
		t.Errorf("assistant tool call mutated: %+v", call.ToolCalls)
	}
	// Wire-format fields survive on the pruned message.
	if l.Messages[oldToolIdx[0]].ToolCallID == "" || l.Messages[oldToolIdx[0]].Name != "read_lines" {
		t.Error("pruned tool message lost ToolCallID/Name")
	}

	// User/assistant text is never touched.
	for _, m := range l.Messages {
		if m.Role != llm.RoleTool && strings.HasPrefix(m.Content, pruneMarkerPrefix) {
			t.Errorf("non-tool message pruned: [%s] %q", m.Role, m.Content)
		}
	}

	// Telemetry event carries the numbers.
	select {
	case ev := <-out:
		pe, ok := ev.(ToolResultsPrunedEvent)
		if !ok {
			t.Fatalf("event = %T, want ToolResultsPrunedEvent", ev)
		}
		if pe.Reclaimed != reclaimed || pe.Pruned == 0 || pe.Estimated != estBefore || pe.Window != 10000 {
			t.Errorf("bad event %+v (reclaimed %d, estBefore %d)", pe, reclaimed, estBefore)
		}
	default:
		t.Error("no ToolResultsPrunedEvent emitted")
	}
}

// TestPrune_MinGainGate: over the trigger threshold but with only
// crumbs reclaimable (the bulk sits in the protected current step),
// prune must NOT rewrite anything — a small gain is not worth a
// KV-cache re-eval.
func TestPrune_MinGainGate(t *testing.T) {
	l := pruneLoop(t, 3000, 100)
	bigToolTurn(l, "old", 3, 450)      // three tiny old results (~125 tok gain each)
	bigToolTurn(l, "current", 1, 5000) // the bulk: protected current step
	if got := l.maybePruneToolResults(context.Background(), nil); got != 0 {
		t.Fatalf("pruned for a marginal gain: %d", got)
	}
	for _, m := range l.Messages {
		if strings.HasPrefix(m.Content, pruneMarkerPrefix) {
			t.Fatal("marker written despite min-gain gate")
		}
	}
}

// TestPrune_SingleGiantTurn_KeepsCurrentStep: an agentic run is ONE
// user turn with many tool steps; prune must still work inside it,
// while the current step's result (everything after the last
// tool-calling assistant message) stays untouchable.
func TestPrune_SingleGiantTurn_KeepsCurrentStep(t *testing.T) {
	l := pruneLoop(t, 10000, 1)
	bigToolTurn(l, "the only turn", 6, 6000)
	if l.maybePruneToolResults(context.Background(), nil) == 0 {
		t.Fatal("expected a prune inside the single giant turn")
	}
	var toolIdx []int
	for i, m := range l.Messages {
		if m.Role == llm.RoleTool {
			toolIdx = append(toolIdx, i)
		}
	}
	last := toolIdx[len(toolIdx)-1]
	if strings.HasPrefix(l.Messages[last].Content, pruneMarkerPrefix) {
		t.Error("current step's result was pruned")
	}
	for _, i := range toolIdx[:len(toolIdx)-1] {
		if !strings.HasPrefix(l.Messages[i].Content, pruneMarkerPrefix) {
			t.Errorf("older result at %d not pruned", i)
		}
	}
}

// TestPrune_AppendOnlyBetweenPrunes: a second pass right after a
// prune (same history, new appended turn) must not rewrite any
// already-visible message — markers stay byte-identical and survivors
// stay verbatim until the NEXT big prune.
func TestPrune_AppendOnlyBetweenPrunes(t *testing.T) {
	l := pruneLoop(t, 10000, 500)
	bigToolTurn(l, "old", 6, 6000)
	bigToolTurn(l, "current", 1, 100)
	if l.maybePruneToolResults(context.Background(), nil) == 0 {
		t.Fatal("expected first prune")
	}
	snapshot := make([]string, len(l.Messages))
	for i, m := range l.Messages {
		snapshot[i] = string(m.Role) + "\x00" + m.Content
	}
	// Next turn appends a bit; the follow-up pass must not touch the
	// prefix (min-gain gate: everything big is already pruned).
	bigToolTurn(l, "next", 1, 200)
	l.maybePruneToolResults(context.Background(), nil)
	for i := range snapshot {
		if got := string(l.Messages[i].Role) + "\x00" + l.Messages[i].Content; got != snapshot[i] {
			t.Fatalf("history rewritten at %d after prune:\nwas %q\nnow %q", i, snapshot[i], got)
		}
	}
}

// TestPrune_Disabled: negative pruneProtect switches the pass off.
func TestPrune_Disabled(t *testing.T) {
	l := pruneLoop(t, 1000, -1)
	bigToolTurn(l, "old", 6, 6000)
	bigToolTurn(l, "current", 1, 100)
	if got := l.maybePruneToolResults(context.Background(), nil); got != 0 {
		t.Fatalf("disabled prune still reclaimed %d", got)
	}
}

// TestPrune_RunsBeforeSummaryFallback: the pre-request defense is
// prune first, summary only if the estimate STILL crosses the generation
// reserve boundary. When prune reclaims enough, the summarizer must not be
// called.
func TestPrune_RunsBeforeSummaryFallback(t *testing.T) {
	l := pruneLoop(t, 10000, 500)
	summarized := false
	l.summarizer = func(ctx context.Context, p llm.Provider, msgs []llm.Message) (string, error) {
		summarized = true
		return "SUMMARY", nil
	}
	bigToolTurn(l, "old", 6, 6000)
	bigToolTurn(l, "current", 1, 100)

	// Same order as the Run loop.
	l.maybePruneToolResults(context.Background(), nil)
	l.maybeAutoCompact(context.Background(), nil, "")

	if summarized {
		t.Error("summarizer called although prune reclaimed enough")
	}
	if got, threshold := l.EstimateNextRequestTokens(), autoCompactThreshold(10_000); got > threshold {
		t.Errorf("request estimate still above compact threshold: %d > %d", got, threshold)
	}
}
