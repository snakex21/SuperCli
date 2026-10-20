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
			{ID: "1", Name: "read_lines", Arguments: `{"file":"internal/agent/loop.go","from":1}`},
			{ID: "2", Name: "write_file", Arguments: `{"path":"internal/agent/prune.go","content":"x"}`},
		}},
		{Role: llm.RoleTool, ToolCallID: "1", Name: "read_lines", Content: "   1 | package agent"},
		{Role: llm.RoleTool, ToolCallID: "2", Name: "write_file", Content: "Created internal/agent/prune.go (1 bytes)"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "3", Name: "edit_line", Arguments: `{"path":"main.go","line":3}`},
			{ID: "4", Name: "search_code", Arguments: `{"query":"foo"}`}, // no path: skipped
			{ID: "5", Name: "read_lines", Arguments: `not json`},         // ignored
		}},
		{Role: llm.RoleTool, ToolCallID: "3", Name: "edit_line", Content: "Edited main.go"},
	}
	got := CompactFacts(msgs, []string{"edit_line", "search_code"})
	for _, want := range []string{
		"files_read: internal/agent/loop.go",
		"files_modified: main.go, internal/agent/prune.go",
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

// The model summary may drop or mislabel the newest requests (a reported bug
// turned into "waiting for the user"). The newest real user messages are
// quoted by code, oldest first, without addon blocks, tool wrappers or a
// prior summary, and without another model call.
func TestAutoSummarizerQuotesRecentUserMessages(t *testing.T) {
	main := &stubProvider{name: "main", scripts: [][]llm.Delta{{
		{Content: "Goal: fix onboarding\nDone: nothing\nState: none\nPending: waiting for the user"},
		{FinishReason: "stop"},
	}}}
	history := []llm.Message{
		{Role: llm.RoleUser, Content: WrapCompactSummary("Goal: older work")},
		{Role: llm.RoleUser, Content: "pierwsza prośba"},
		{Role: llm.RoleAssistant, Content: "ok"},
		llm.MessageWithContextBlocks(llm.RoleUser, []llm.ContextBlock{{Source: llm.SourceHuman, Text: "druga prośba"}, {Source: llm.SourceMemory, Text: "[relevant_previous_sessions]\nprivate recall\n[/relevant_previous_sessions]"}}),
		{Role: llm.RoleUser, Content: "Attached image from tool read_image:"},
		{Role: llm.RoleUser, Content: "<task-notification>worker done</task-notification>"},
		{Role: llm.RoleUser, Content: "Termux: setup przy pierwszym uruchomieniu daje błąd,   a /providers działa"},
		{Role: llm.RoleAssistant, Content: "sprawdzę"},
	}
	got, err := NewAutoSummarizer(nil)(context.Background(), main, history)
	if err != nil {
		t.Fatal(err)
	}
	carry := decodeCompactUserContext(t, got)
	if len(carry.Requests) != 3 || carry.Requests[0] != "pierwsza prośba" || carry.Requests[1] != "druga prośba" || carry.Requests[2] != history[6].Content {
		t.Fatal("full original user messages not preserved in order")
	}
	for _, leaked := range []string{"private recall", "Attached image", "task-notification", "older work\n- "} {
		if strings.Contains(strings.Join(carry.Requests, "\n"), leaked) {
			t.Fatalf("quoted block leaked %q:\n%s", leaked, got)
		}
	}
	if atomic.LoadInt32(&main.calls) != 1 {
		t.Fatalf("calls=%d, want one summary call", main.calls)
	}

}
