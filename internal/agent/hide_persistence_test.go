package agent

import (
	"context"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// These tests pin the DURABILITY of hidden flags across Runs.
// Audit finding (2026-07-12): Run() used to call resetHidden()
// at the top, so a /clear (HideLastUserTurns) or hide_messages
// (HideRange) issued between Runs was silently undone and the
// full history went back to the provider on the next request —
// /clear did not work between messages and the KV-cache prefix
// was invalidated. Hides must survive until the message indices
// they refer to are themselves invalidated (compaction, /resume).

// runAndDrain runs one prompt through the loop and waits for the
// event channel to close.
func runAndDrain(t *testing.T, l *Loop, prompt string) {
	t.Helper()
	ch, err := l.Run(context.Background(), prompt)
	if err != nil {
		t.Fatalf("Run(%q): %v", prompt, err)
	}
	drainEvents(t, ch)
}

// providerSawContent reports whether any message sent to the
// capture provider on its LAST call contains substr.
func providerSawContent(p *captureProvider, substr string) bool {
	for _, m := range p.messages {
		if strings.Contains(m.Content, substr) {
			return true
		}
	}
	return false
}

func TestRun_ClearBetweenRuns_HidesReachProvider(t *testing.T) {
	p := &captureProvider{name: "capture"}
	l := makeLoop(t, p, tools.NewRegistry(), "sys")

	// Turn 1 and 2: build up history the user will then /clear.
	runAndDrain(t, l, "SECRET-OLD-QUESTION-ONE")
	runAndDrain(t, l, "SECRET-OLD-QUESTION-TWO")

	// /clear between Runs (exactly what main.go's "clear" command
	// does): keep the last 2 user turns, hide everything older.
	// Here it hides turn one.
	if hidden := l.HideLastUserTurns(1); hidden == 0 {
		t.Fatal("HideLastUserTurns(1) hid nothing; test setup broken")
	}

	// Next user message: the hidden turn must NOT go to the provider.
	runAndDrain(t, l, "fresh question")

	if providerSawContent(p, "SECRET-OLD-QUESTION-ONE") {
		t.Fatal("/clear did not survive the next Run: hidden turn was sent to the provider")
	}
	if !providerSawContent(p, "[earlier context cleared") {
		t.Error("expected the collapsed-placeholder message in the provider prompt")
	}
	if !providerSawContent(p, "fresh question") {
		t.Error("the new user message should reach the provider")
	}
}

func TestRun_HideRangeBetweenRuns_Persists(t *testing.T) {
	// hide_messages tool path: it calls HideRange on the loop.
	p := &captureProvider{name: "capture"}
	l := makeLoop(t, p, tools.NewRegistry(), "sys")

	runAndDrain(t, l, "SECRET-HIDDEN-BY-TOOL")

	// Hide the whole first turn (user + assistant), sparing the
	// leading system message, as a hide_messages call would.
	sys := 0
	for sys < len(l.Messages) && l.Messages[sys].Role == llm.RoleSystem {
		sys++
	}
	if err := l.HideRange(sys, len(l.Messages)); err != nil {
		t.Fatalf("HideRange: %v", err)
	}

	runAndDrain(t, l, "next prompt")

	if providerSawContent(p, "SECRET-HIDDEN-BY-TOOL") {
		t.Fatal("hide_messages range did not survive the next Run")
	}
}

func TestRun_EvictForBudget_HidesPersistAcrossRuns(t *testing.T) {
	p := &captureProvider{name: "capture"}
	l := makeLoop(t, p, tools.NewRegistry(), "sys")

	runAndDrain(t, l, "SECRET-EVICTED-"+strings.Repeat("x", 3000))
	runAndDrain(t, l, "second turn")

	// Budget eviction (as the loop does after each step).
	l.creditTracker = &fakeCredit{cap: 500} // threshold 400
	out := make(chan Event, 8)
	defer close(out)
	if n := l.EvictForBudget(context.Background(), out); n == 0 {
		t.Fatal("EvictForBudget evicted nothing; test setup broken")
	}
	if !l.isHidden(l.firstNonSystemIndex()) {
		t.Fatal("oldest non-system message should be hidden after eviction")
	}
	l.creditTracker = nil // don't re-evict inside the next Run

	runAndDrain(t, l, "third turn")

	if providerSawContent(p, "SECRET-EVICTED-") {
		t.Fatal("budget eviction did not survive the next Run: evicted content was sent to the provider")
	}
}

// isHidden / firstNonSystemIndex are tiny test helpers on Loop.
func (l *Loop) isHidden(i int) bool {
	return i >= 0 && i < len(l.hidden) && l.hidden[i]
}

func (l *Loop) firstNonSystemIndex() int {
	for i, m := range l.Messages {
		if m.Role != llm.RoleSystem {
			return i
		}
	}
	return -1
}

func TestCompaction_StillResetsHidden(t *testing.T) {
	// Compaction rewrites l.Messages, so old hidden indices are
	// meaningless — CompactWithSummary must keep resetting them.
	l := makeLoopWithMessages(
		llm.Message{Role: llm.RoleSystem, Content: "sys"},
		llm.Message{Role: llm.RoleUser, Content: "u1"},
		llm.Message{Role: llm.RoleAssistant, Content: "a1"},
	)
	_ = l.HideRange(1, 2)
	l.CompactWithSummary("summary of it all")
	if l.HiddenCount() != 0 {
		t.Errorf("hidden after compaction = %d, want 0 (indices invalidated)", l.HiddenCount())
	}
}
