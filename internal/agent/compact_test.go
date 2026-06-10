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
	if l.Messages[1].Role != llm.RoleSystem || !strings.Contains(l.Messages[1].Content, "the summary") {
		t.Errorf("summary message = %q (%s)", l.Messages[1].Content, l.Messages[1].Role)
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
