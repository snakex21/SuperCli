package agent

import (
	"strings"
	"testing"

	"supercli/internal/llm"
)

// Tests for the F11 draft-context improvement: the draft model
// must receive a trimmed slice of recent conversation, not just
// the bare last user prompt.

func TestDraftPrompt_NoHistoryFallsBackToUserPrompt(t *testing.T) {
	l := &Loop{Messages: []llm.Message{
		{Role: llm.RoleUser, Content: "fix the report"},
	}}
	if got := l.draftPrompt(); got != "fix the report" {
		t.Fatalf("got %q", got)
	}
}

func TestDraftPrompt_NoUserMessage(t *testing.T) {
	l := &Loop{Messages: []llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
	}}
	if got := l.draftPrompt(); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

func TestDraftPrompt_IncludesRecentContext(t *testing.T) {
	l := &Loop{Messages: []llm.Message{
		{Role: llm.RoleUser, Content: "open report.docx and fix the title"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{Name: "read_docx"}}},
		{Role: llm.RoleTool, Content: "Quarterly Reprot\nSection one..."},
		{Role: llm.RoleUser, Content: "now also fix the typo"},
	}}
	got := l.draftPrompt()
	if !strings.Contains(got, "Current request: now also fix the typo") {
		t.Fatalf("missing current request: %q", got)
	}
	if !strings.Contains(got, "read_docx") {
		t.Fatalf("missing tool-call name: %q", got)
	}
	if !strings.Contains(got, "Quarterly Reprot") {
		t.Fatalf("missing tool output: %q", got)
	}
	if !strings.Contains(got, "open report.docx") {
		t.Fatalf("missing earlier user message: %q", got)
	}
}

func TestDraftPrompt_TruncatesToolOutput(t *testing.T) {
	long := strings.Repeat("x", 5000)
	l := &Loop{Messages: []llm.Message{
		{Role: llm.RoleUser, Content: "summarize"},
		{Role: llm.RoleTool, Content: long},
		{Role: llm.RoleUser, Content: "go on"},
	}}
	got := l.draftPrompt()
	if len(got) > 2500 {
		t.Fatalf("draft prompt too long (%d chars) — truncation failed", len(got))
	}
	if !strings.Contains(got, "...[truncated]") {
		t.Fatalf("missing truncation marker: %q", got)
	}
}

func TestDraftPrompt_CapsMessageCount(t *testing.T) {
	var msgs []llm.Message
	for i := 0; i < 30; i++ {
		msgs = append(msgs, llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("m", 10)})
	}
	msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: "do it"})
	l := &Loop{Messages: msgs}
	got := l.draftPrompt()
	if n := strings.Count(got, "assistant: "); n > draftContextMessages {
		t.Fatalf("too many context messages: %d", n)
	}
}

func TestDraftPrompt_SkipsSystemMessages(t *testing.T) {
	l := &Loop{Messages: []llm.Message{
		{Role: llm.RoleUser, Content: "first"},
		{Role: llm.RoleSystem, Content: "[draft plan] secret old plan"},
		{Role: llm.RoleUser, Content: "second"},
	}}
	if got := l.draftPrompt(); strings.Contains(got, "secret old plan") {
		t.Fatalf("system message leaked into draft context: %q", got)
	}
}
