package agent

import (
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestCompactWithSummary_KeepsLeadingSystem(t *testing.T) {
	l := makeLoopWithMessages(
		llm.Message{Role: llm.RoleSystem, Content: "base prompt"},
		llm.Message{Role: llm.RoleUser, Content: "u1"},
		llm.Message{Role: llm.RoleAssistant, Content: "a1"},
		llm.Message{Role: llm.RoleUser, Content: "u2"},
	)
	removed := l.CompactWithSummary("the summary")
	if removed != 3 {
		t.Errorf("removed = %d, want 3", removed)
	}
	if len(l.Messages) != 2 {
		t.Fatalf("Messages length = %d, want 2 (base system + summary)", len(l.Messages))
	}
	if l.Messages[0].Content != "base prompt" {
		t.Errorf("leading system message = %q, want it preserved", l.Messages[0].Content)
	}
	if l.Messages[1].Role != llm.RoleUser || !strings.Contains(l.Messages[1].Content, "the summary") {
		t.Errorf("summary message = %q (%s)", l.Messages[1].Content, l.Messages[1].Role)
	}
}

// TestCompactPrefixWithSummary_KeepsTail: only the messages before
// the cut are replaced; the tail (the last user turn) survives
// verbatim after the summary.
func TestCompactPrefixWithSummary_KeepsTail(t *testing.T) {
	l := makeLoopWithMessages(
		llm.Message{Role: llm.RoleSystem, Content: "base prompt"},
		llm.Message{Role: llm.RoleUser, Content: "u1"},
		llm.Message{Role: llm.RoleAssistant, Content: "a1"},
		llm.Message{Role: llm.RoleUser, Content: "u2 (current turn)"},
		llm.Message{Role: llm.RoleAssistant, Content: "a2 in progress"},
	)
	removed := l.CompactPrefixWithSummary("the summary", 3)
	if removed != 2 {
		t.Errorf("removed = %d, want 2 (u1, a1)", removed)
	}
	want := []string{"base prompt", "the summary", "u2 (current turn)", "a2 in progress"}
	if len(l.Messages) != len(want) {
		t.Fatalf("Messages length = %d, want %d: %+v", len(l.Messages), len(want), l.Messages)
	}
	for i, w := range want {
		if l.Messages[i].Content != w {
			t.Errorf("Messages[%d] = %q, want %q", i, l.Messages[i].Content, w)
		}
	}
	if l.Messages[1].Role != llm.RoleUser {
		t.Errorf("summary role = %s, want user (mid-history system 400s strict chat templates)", l.Messages[1].Role)
	}
}

// TestCompactPrefixWithSummary_ClampsRange: out-of-range cuts clamp
// instead of panicking; a cut inside the leading system run behaves
// like "nothing removed".
func TestCompactPrefixWithSummary_ClampsRange(t *testing.T) {
	l := makeLoopWithMessages(
		llm.Message{Role: llm.RoleSystem, Content: "sys"},
		llm.Message{Role: llm.RoleUser, Content: "u1"},
	)
	if removed := l.CompactPrefixWithSummary("s", 99); removed != 1 {
		t.Errorf("upto beyond len: removed = %d, want 1", removed)
	}
	l2 := makeLoopWithMessages(
		llm.Message{Role: llm.RoleSystem, Content: "sys"},
		llm.Message{Role: llm.RoleUser, Content: "u1"},
	)
	if removed := l2.CompactPrefixWithSummary("s", 0); removed != 0 {
		t.Errorf("upto inside system run: removed = %d, want 0", removed)
	}
	if l2.Messages[len(l2.Messages)-1].Content != "u1" {
		t.Errorf("tail lost: %+v", l2.Messages)
	}
}

func TestCompactWithSummary_ResetsHidden(t *testing.T) {
	l := makeLoopWithMessages(
		llm.Message{Role: llm.RoleUser, Content: "u1"},
		llm.Message{Role: llm.RoleAssistant, Content: "a1"},
	)
	if err := l.HideRange(1, 2); err != nil {
		t.Fatalf("HideRange: %v", err)
	}
	l.CompactWithSummary("s")
	if l.HiddenCount() != 0 {
		t.Errorf("HiddenCount = %d, want 0 after compaction", l.HiddenCount())
	}
	if len(l.Messages) != 1 {
		t.Errorf("Messages length = %d, want 1 (just the summary)", len(l.Messages))
	}
}
