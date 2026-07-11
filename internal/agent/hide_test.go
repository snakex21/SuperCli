package agent

import (
	"context"
	"strings"
	"testing"

	"supercli/internal/llm"
)

// makeLoopWithMessages builds a Loop with the given
// messages, no provider, and a no-op writer. Useful for
// testing the F14 hide mechanics in isolation.
func makeLoopWithMessages(msgs ...llm.Message) *Loop {
	return &Loop{Messages: append([]llm.Message(nil), msgs...)}
}

func TestHideRange_BasicMark(t *testing.T) {
	l := makeLoopWithMessages(
		llm.Message{Role: llm.RoleUser, Content: "u1"},
		llm.Message{Role: llm.RoleAssistant, Content: "a1"},
		llm.Message{Role: llm.RoleUser, Content: "u2"},
		llm.Message{Role: llm.RoleAssistant, Content: "a2"},
	)
	if err := l.HideRange(0, 2); err != nil {
		t.Fatalf("HideRange: %v", err)
	}
	if l.HiddenCount() != 2 {
		t.Errorf("hidden = %d, want 2", l.HiddenCount())
	}
	// l.Messages unchanged
	if len(l.Messages) != 4 {
		t.Errorf("Messages length = %d, want 4 (HideRange should NOT remove)", len(l.Messages))
	}
}

func TestHideRange_InvalidRange(t *testing.T) {
	l := makeLoopWithMessages(
		llm.Message{Role: llm.RoleUser, Content: "u1"},
		llm.Message{Role: llm.RoleAssistant, Content: "a1"},
	)
	cases := []struct {
		from, to int
	}{
		{-1, 1},   // from < 0
		{0, 5},    // to > len
		{2, 1},    // from > to
		{0, 0},    // no-op, NOT an error
	}
	for _, c := range cases {
		err := l.HideRange(c.from, c.to)
		if c.from == 0 && c.to == 0 {
			// no-op is allowed
			if err != nil {
				t.Errorf("[%d, %d) err = %v, want nil (no-op)", c.from, c.to, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("[%d, %d) err = nil, want non-nil", c.from, c.to)
		}
	}
}

func TestVisibleMessages_CollapsesConsecutive(t *testing.T) {
	l := makeLoopWithMessages(
		llm.Message{Role: llm.RoleUser, Content: "u1"},
		llm.Message{Role: llm.RoleAssistant, Content: "a1"},
		llm.Message{Role: llm.RoleUser, Content: "u2"},
		llm.Message{Role: llm.RoleAssistant, Content: "a2"},
		llm.Message{Role: llm.RoleUser, Content: "u3"},
	)
	// Hide the middle two (a1 and u2)
	if err := l.HideRange(1, 3); err != nil {
		t.Fatalf("HideRange: %v", err)
	}
	vis := l.VisibleMessages()
	if len(vis) != 4 {
		// u1, [placeholder], a2, u3  -- 4 entries
		t.Fatalf("VisibleMessages length = %d, want 4 (u1, placeholder, a2, u3)", len(vis))
	}
	if vis[0].Content != "u1" {
		t.Errorf("vis[0] = %q, want u1", vis[0].Content)
	}
	if !strings.Contains(vis[1].Content, "[earlier context cleared") {
		t.Errorf("vis[1] placeholder = %q", vis[1].Content)
	}
	if !strings.Contains(vis[1].Content, "2 message(s)") {
		t.Errorf("vis[1] should say '2 message(s)', got %q", vis[1].Content)
	}
	if vis[1].Role != llm.RoleUser {
		t.Errorf("vis[1] role = %v, want user (mid-history system 400s strict chat templates)", vis[1].Role)
	}
	if vis[2].Content != "a2" {
		t.Errorf("vis[2] = %q, want a2", vis[2].Content)
	}
	if vis[3].Content != "u3" {
		t.Errorf("vis[3] = %q, want u3", vis[3].Content)
	}
}

func TestVisibleMessages_NoHidesIsIdentity(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "u1"},
		{Role: llm.RoleAssistant, Content: "a1"},
	}
	l := makeLoopWithMessages(msgs...)
	vis := l.VisibleMessages()
	if len(vis) != 2 {
		t.Fatalf("len = %d, want 2", len(vis))
	}
	if vis[0].Content != "u1" || vis[1].Content != "a1" {
		t.Errorf("identity broken: %+v", vis)
	}
}

func TestVisibleMessages_AllHidden(t *testing.T) {
	l := makeLoopWithMessages(
		llm.Message{Role: llm.RoleUser, Content: "u1"},
		llm.Message{Role: llm.RoleAssistant, Content: "a1"},
	)
	if err := l.HideRange(0, 2); err != nil {
		t.Fatalf("HideRange: %v", err)
	}
	vis := l.VisibleMessages()
	if len(vis) != 1 {
		t.Fatalf("len = %d, want 1 (single placeholder)", len(vis))
	}
	if !strings.Contains(vis[0].Content, "2 message(s)") {
		t.Errorf("placeholder = %q, want '2 message(s)'", vis[0].Content)
	}
}

func TestVisibleMessages_AdjacentRangesSinglePlaceholder(t *testing.T) {
	l := makeLoopWithMessages(
		llm.Message{Role: llm.RoleUser, Content: "u1"},
		llm.Message{Role: llm.RoleAssistant, Content: "a1"},
		llm.Message{Role: llm.RoleUser, Content: "u2"},
	)
	// Hide [0, 1) and [1, 2) — adjacent ranges, should
	// collapse into one placeholder
	_ = l.HideRange(0, 1)
	_ = l.HideRange(1, 2)
	vis := l.VisibleMessages()
	if len(vis) != 2 {
		t.Fatalf("len = %d, want 2 (placeholder, u2)", len(vis))
	}
	if !strings.Contains(vis[0].Content, "2 message(s)") {
		t.Errorf("placeholder = %q, want '2 message(s)' (adjacent collapsed)", vis[0].Content)
	}
	if vis[1].Content != "u2" {
		t.Errorf("vis[1] = %q, want u2", vis[1].Content)
	}
}

func TestEstimateVisibleTokens_MatchesUnifiedEstimator(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: strings.Repeat("a", 400)},
		{Role: llm.RoleAssistant, Content: strings.Repeat("b", 200)},
	}
	l := makeLoopWithMessages(msgs...)
	got := l.EstimateVisibleTokens()
	want := llm.EstimateTokens(msgs) // 400/3+16 + 200/3+16 = 231
	if got != want {
		t.Errorf("EstimateVisibleTokens = %d, want %d (unified llm estimator)", got, want)
	}
}

func TestEstimateVisibleTokens_SkipsHidden(t *testing.T) {
	l := makeLoopWithMessages(
		llm.Message{Role: llm.RoleUser, Content: strings.Repeat("a", 400)},
		llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("b", 400)}, // hidden
		llm.Message{Role: llm.RoleUser, Content: strings.Repeat("c", 400)},
	)
	allVisible := l.EstimateVisibleTokens()
	_ = l.HideRange(1, 2)
	got := l.EstimateVisibleTokens()
	if got == 0 {
		t.Fatal("got 0, expected > 0")
	}
	// The hidden 400-char message (~149 tok) is replaced by a short
	// placeholder (~35 tok); the estimate must drop accordingly.
	if got >= allVisible-50 {
		t.Errorf("EstimateVisibleTokens = %d, want well below %d (hidden message should be replaced by short placeholder)", got, allVisible)
	}
}

func TestHideLastUserTurns_KeepsRecent(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "u1"},
		{Role: llm.RoleAssistant, Content: "a1"},
		{Role: llm.RoleUser, Content: "u2"},
		{Role: llm.RoleAssistant, Content: "a2"},
		{Role: llm.RoleUser, Content: "u3"},
		{Role: llm.RoleAssistant, Content: "a3"},
	}
	l := makeLoopWithMessages(msgs...)
	hidden := l.HideLastUserTurns(2) // keep last 2 user turns (u2, u3)
	if hidden != 2 {
		t.Errorf("hidden = %d, want 2 (u1, a1)", hidden)
	}
	vis := l.VisibleMessages()
	// u2, a2, u3, a3 -- 4 entries (no placeholder, the
	// hidden [u1, a1] is at the very start so it gets
	// flushed to a placeholder)
	if len(vis) != 5 {
		t.Fatalf("len = %d, want 5 (placeholder + u2 + a2 + u3 + a3)", len(vis))
	}
	if !strings.Contains(vis[0].Content, "2 message(s)") {
		t.Errorf("vis[0] placeholder = %q, want '2 message(s)'", vis[0].Content)
	}
	if vis[1].Content != "u2" {
		t.Errorf("vis[1] = %q, want u2", vis[1].Content)
	}
	if vis[4].Content != "a3" {
		t.Errorf("vis[4] = %q, want a3", vis[4].Content)
	}
}

func TestHideLastUserTurns_KeepAllNoOp(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "u1"},
		{Role: llm.RoleAssistant, Content: "a1"},
		{Role: llm.RoleUser, Content: "u2"},
	}
	l := makeLoopWithMessages(msgs...)
	hidden := l.HideLastUserTurns(5) // keep more than exist
	if hidden != 0 {
		t.Errorf("hidden = %d, want 0 (nothing to hide)", hidden)
	}
}

func TestEvictForBudget_NoTrackerNoOp(t *testing.T) {
	l := makeLoopWithMessages(
		llm.Message{Role: llm.RoleUser, Content: strings.Repeat("a", 1000)},
	)
	out := make(chan Event, 8)
	defer close(out)
	if n := l.EvictForBudget(context.Background(), out); n != 0 {
		t.Errorf("evicted = %d, want 0 (no credit tracker)", n)
	}
}

func TestEvictForBudget_TrimsOldestNonSystem(t *testing.T) {
	l := makeLoopWithMessages(
		llm.Message{Role: llm.RoleUser, Content: strings.Repeat("a", 1000)},
		llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("b", 1000)},
		llm.Message{Role: llm.RoleUser, Content: strings.Repeat("c", 1000)},
		llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("d", 1000)},
	)
	// Stub credit tracker: session cap 500, threshold 400
	l.creditTracker = &fakeCredit{cap: 500}
	out := make(chan Event, 8)
	defer close(out)
	// Visible token estimate is well above the threshold
	// (cap 500 * 0.8 = 400), so eviction must kick in.
	evicted := l.EvictForBudget(context.Background(), out)
	if evicted < 1 {
		t.Errorf("evicted = %d, want >= 1", evicted)
	}
	// System messages should never be evicted
	for i, m := range l.Messages {
		if m.Role == llm.RoleSystem {
			if i < len(l.hidden) && l.hidden[i] {
				t.Errorf("system message at %d got hidden", i)
			}
		}
	}
	// Drain the event channel
	for i := 0; i < evicted; i++ {
		select {
		case ev := <-out:
			mh, ok := ev.(MessagesHiddenEvent)
			if !ok {
				t.Errorf("event %d is %T, want MessagesHiddenEvent", i, ev)
			}
			if mh.Reason != "budget" {
				t.Errorf("reason = %q, want budget", mh.Reason)
			}
		default:
		}
	}
}

func TestEvictForBudget_NoEvictionUnderThreshold(t *testing.T) {
	l := makeLoopWithMessages(
		llm.Message{Role: llm.RoleUser, Content: "tiny"},
		llm.Message{Role: llm.RoleAssistant, Content: "tiny"},
	)
	l.creditTracker = &fakeCredit{cap: 10000} // threshold 8000
	out := make(chan Event, 8)
	defer close(out)
	if n := l.EvictForBudget(context.Background(), out); n != 0 {
		t.Errorf("evicted = %d, want 0 (way under threshold)", n)
	}
}

func TestEvictForBudget_NoCapNoOp(t *testing.T) {
	l := makeLoopWithMessages(
		llm.Message{Role: llm.RoleUser, Content: strings.Repeat("a", 100000)},
		llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("b", 100000)},
	)
	// cap == 0 means unlimited: never evict, no matter how
	// large the visible context or how much was already used.
	l.creditTracker = &fakeCredit{cap: 0, used: 1 << 30}
	out := make(chan Event, 8)
	defer close(out)
	if n := l.EvictForBudget(context.Background(), out); n != 0 {
		t.Errorf("evicted = %d, want 0 (cap 0 = unlimited)", n)
	}
	if l.HiddenCount() != 0 {
		t.Errorf("hidden = %d, want 0", l.HiddenCount())
	}
}

// trackerNoCap is a CreditTracker that does not implement
// sessionCapper at all.
type trackerNoCap struct{}

func (trackerNoCap) Record(_ context.Context, _, _ int64, _ string) error { return nil }
func (trackerNoCap) Used() (session, daily int64)                         { return 1 << 30, 0 }

func TestEvictForBudget_TrackerWithoutCapNoOp(t *testing.T) {
	l := makeLoopWithMessages(
		llm.Message{Role: llm.RoleUser, Content: strings.Repeat("a", 100000)},
	)
	l.creditTracker = trackerNoCap{}
	out := make(chan Event, 8)
	defer close(out)
	if n := l.EvictForBudget(context.Background(), out); n != 0 {
		t.Errorf("evicted = %d, want 0 (tracker exposes no cap)", n)
	}
}

func TestResetHidden(t *testing.T) {
	l := makeLoopWithMessages(
		llm.Message{Role: llm.RoleUser, Content: "u1"},
		llm.Message{Role: llm.RoleAssistant, Content: "a1"},
	)
	_ = l.HideRange(0, 1)
	if l.HiddenCount() != 1 {
		t.Fatalf("hidden before reset = %d, want 1", l.HiddenCount())
	}
	l.resetHidden()
	if l.HiddenCount() != 0 {
		t.Errorf("hidden after reset = %d, want 0", l.HiddenCount())
	}
	// And the underlying slice is nil, not just empty
	if l.hidden != nil {
		t.Errorf("hidden should be nil after reset, got len=%d", len(l.hidden))
	}
}

// fakeCredit is a stub credit tracker for F14 tests.
type fakeCredit struct {
	used int64
	cap  int64
}

func (f *fakeCredit) SessionCap() int64 { return f.cap }

func (f *fakeCredit) Record(_ context.Context, in, out int64, _ string) error {
	f.used += in + out
	return nil
}
func (f *fakeCredit) Used() (session, daily int64) {
	return f.used, 0
}
