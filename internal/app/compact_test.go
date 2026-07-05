package app

import (
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestClampSummary_ShortUntouched(t *testing.T) {
	if got := clampSummary("Goal: x\nDone: y"); got != "Goal: x\nDone: y" {
		t.Errorf("short summary modified: %q", got)
	}
}

func TestClampSummary_CutsAtLineBoundary(t *testing.T) {
	line := strings.Repeat("a", 99) + "\n"
	long := strings.Repeat(line, 60) // 6000 chars
	got := clampSummary(long)
	if len(got) > compactSummaryMaxChars+len("\n[summary truncated]") {
		t.Errorf("clamped summary too long: %d chars", len(got))
	}
	if !strings.HasSuffix(got, "[summary truncated]") {
		t.Errorf("missing truncation marker: %q", got[len(got)-40:])
	}
	// Cut on a line boundary: the last content line is intact.
	body := strings.TrimSuffix(got, "\n[summary truncated]")
	lines := strings.Split(body, "\n")
	if l := lines[len(lines)-1]; l != strings.Repeat("a", 99) {
		t.Errorf("cut mid-line: last line %q (len %d)", l, len(l))
	}
}

func TestCompactFacts_PathsAndTools(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "1", Name: "read_lines", Arguments: `{"path":"internal/agent/loop.go","from":1}`},
			{ID: "2", Name: "write_file", Arguments: `{"path":"internal/agent/prune.go","content":"x"}`},
		}},
		{Role: llm.RoleTool, ToolCallID: "1", Name: "read_lines", Content: "..."},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "3", Name: "edit_line", Arguments: `{"path":"main.go","line":3}`},
			{ID: "4", Name: "search_code", Arguments: `{"query":"foo"}`}, // no path: skipped
			{ID: "5", Name: "read_lines", Arguments: `not json`},         // ignored
		}},
	}
	got := compactFacts(msgs, []string{"edit_line", "search_code"})
	for _, want := range []string{
		"files_read: internal/agent/loop.go",
		"files_modified: internal/agent/prune.go, main.go",
		"loaded_tools: edit_line, search_code",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("compactFacts missing %q in:%s", want, got)
		}
	}
}

func TestCompactFacts_EmptyIsEmpty(t *testing.T) {
	if got := compactFacts([]llm.Message{{Role: llm.RoleUser, Content: "hi"}}, nil); got != "" {
		t.Errorf("expected empty facts, got %q", got)
	}
}
