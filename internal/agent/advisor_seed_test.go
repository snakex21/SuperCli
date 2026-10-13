package agent

import (
	"strings"
	"testing"

	"supercli/internal/llm"
)

// TestAdvisorSeed_TailWithRequestAndClippedTools: the advisor gets the last
// user request, a recent tail that never starts on an orphaned tool result,
// and tool outputs clipped to advisorSeedToolBytes.
func TestAdvisorSeed_TailWithRequestAndClippedTools(t *testing.T) {
	msgs := []llm.Message{{Role: llm.RoleUser, Content: "fix the bug"}}
	big := strings.Repeat("ż", advisorSeedToolBytes) // 2 bytes per rune
	for i := 0; i < 10; i++ {
		msgs = append(msgs,
			llm.Message{Role: llm.RoleAssistant, Content: "step"},
			llm.Message{Role: llm.RoleTool, ToolCallID: "c", Content: big},
		)
	}
	out := advisorSeed(msgs)
	if out[0].Role != llm.RoleUser || out[0].Content != "fix the bug" {
		t.Fatalf("first message must be the user request, got %+v", out[0].Role)
	}
	if len(out) > advisorSeedMessages+1 {
		t.Fatalf("seed too long: %d", len(out))
	}
	if out[1].Role == llm.RoleTool {
		t.Fatalf("tail must not start on an orphaned tool result")
	}
	for _, m := range out {
		if m.Role != llm.RoleTool {
			continue
		}
		if !strings.HasSuffix(m.Content, "[… clipped for advisor]") || len(m.Content) > advisorSeedToolBytes+40 {
			t.Fatalf("tool output not clipped: %d bytes", len(m.Content))
		}
		if !strings.HasPrefix(m.Content, "żż") || strings.ContainsRune(m.Content, '\uFFFD') {
			t.Fatalf("clip broke UTF-8")
		}
	}
	if msgs[2].Content != big {
		t.Fatalf("advisorSeed must not mutate the parent's messages")
	}
}

// TestAdvisorSeed_ShortHistoryUnchanged: a short history passes through
// without duplicating the user request.
func TestAdvisorSeed_ShortHistoryUnchanged(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "q"},
		{Role: llm.RoleAssistant, Content: "a"},
	}
	out := advisorSeed(msgs)
	if len(out) != 2 || out[0].Content != "q" || out[1].Content != "a" {
		t.Fatalf("short history changed: %+v", out)
	}
}
