package agent

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"supercli/internal/llm"
)

type failingSummaryProvider struct{ calls atomic.Int32 }

func (p *failingSummaryProvider) Name() string         { return "broken-compact" }
func (p *failingSummaryProvider) SupportsVision() bool { return false }
func (p *failingSummaryProvider) Complete(context.Context, []llm.Message, []llm.ToolDef) (<-chan llm.Delta, error) {
	p.calls.Add(1)
	return nil, errors.New("side backend unavailable")
}

func TestClampSummary_ShortUntouched(t *testing.T) {
	if got := ClampSummary("Goal: x\nDone: y"); got != "Goal: x\nDone: y" {
		t.Errorf("short summary modified: %q", got)
	}
}

func TestClampSummary_CutsAtLineBoundary(t *testing.T) {
	line := strings.Repeat("a", 99) + "\n"
	long := strings.Repeat(line, 60) // 6000 chars
	got := ClampSummary(long)
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
	got := CompactFacts(msgs, []string{"edit_line", "search_code"})
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
	if got := CompactFacts([]llm.Message{{Role: llm.RoleUser, Content: "hi"}}, nil); got != "" {
		t.Errorf("expected empty facts, got %q", got)
	}
}

func TestAutoSummarizerCompactModelFallsBackToActiveProvider(t *testing.T) {
	side := &failingSummaryProvider{}
	main := &stubProvider{name: "main", scripts: [][]llm.Delta{{
		{Content: "Goal: keep working\nDone: old investigation summarized"},
		{FinishReason: "stop"},
	}}}
	summarize := NewAutoSummarizerWithProvider(side, nil)
	got, err := summarize(context.Background(), main, []llm.Message{{Role: llm.RoleUser, Content: "old work"}})
	if err != nil {
		t.Fatal(err)
	}
	if side.calls.Load() != 1 || atomic.LoadInt32(&main.calls) != 1 {
		t.Fatalf("calls side=%d main=%d, want one failed side call and one fallback", side.calls.Load(), atomic.LoadInt32(&main.calls))
	}
	if !strings.Contains(got, "old investigation summarized") {
		t.Fatalf("fallback summary missing: %q", got)
	}
}
