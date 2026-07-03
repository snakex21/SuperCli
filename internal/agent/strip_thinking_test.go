package agent

import (
	"context"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestStripThinking_RemovesBlocks(t *testing.T) {
	cases := []struct{ in, want string }{
		{"<thinking>plan</thinking>answer", "answer"},
		{"a <think>x</think> b", "a  b"},
		{"<reasoning>blah</reasoning>final", "final"},
		{"no tags here", "no tags here"},
		{"before\n<thinking>\nl1\nl2\n</thinking>\nafter", "before\n\nafter"},
		{"<thinking>truncated stream never closed", ""},
	}
	for _, c := range cases {
		if got := stripThinking(c.in); got != c.want {
			t.Errorf("stripThinking(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestStripThinkingFromMessage_DropsEmptiedTextPart guards the tool-call
// case: a turn whose only text is a <thinking> block (model reasoned,
// then emitted a tool call with no visible answer) must NOT leave an
// empty text part, which the provider rejects on the next request. The
// tool call is preserved.
func TestStripThinkingFromMessage_DropsEmptiedTextPart(t *testing.T) {
	msg := llm.Message{
		Role:  llm.RoleAssistant,
		Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: "<thinking>which tool?</thinking>"}},
		ToolCalls: []llm.ToolCall{
			{ID: "1", Name: "ctx_execute", Arguments: `{"command":["ls"]}`},
		},
	}
	got := stripThinkingFromMessage(msg)
	for i, p := range got.Parts {
		if p.Type == llm.PartTypeText && p.Text == "" {
			t.Errorf("part %d is an empty text part; want it dropped", i)
		}
	}
	if len(got.ToolCalls) != 1 {
		t.Fatalf("tool call lost: %+v", got.ToolCalls)
	}
}

// TestLoop_HistoryStripsThinkingButStorageKeepsIt proves the Task 2b
// invariant: the in-memory history that drives the next request carries
// only the final answer, while the session store keeps the full text
// (with <thinking>) for UI replay.
func TestLoop_HistoryStripsThinkingButStorageKeepsIt(t *testing.T) {
	prov := makeScriptedProvider("<thinking>let me reason at length</thinking>\nThe answer is 42.")
	reg := emptyRegistry()
	w := &recordingWriter{}
	loop, err := NewLoop(LoopConfig{Provider: prov, Registry: reg, Writer: w})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	events, err := loop.Run(context.Background(), "what is 6*7?")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for range events {
	}

	// In-memory history: last message is the assistant answer, stripped.
	var lastAssistant *llm.Message
	for i := range loop.Messages {
		if loop.Messages[i].Role == llm.RoleAssistant {
			lastAssistant = &loop.Messages[i]
		}
	}
	if lastAssistant == nil {
		t.Fatal("no assistant message in history")
	}
	histText := lastAssistant.Content
	for _, p := range lastAssistant.Parts {
		histText += p.Text
	}
	if strings.Contains(histText, "<thinking>") || strings.Contains(histText, "let me reason") {
		t.Errorf("history assistant message still carries thinking: %q", histText)
	}
	if !strings.Contains(histText, "The answer is 42.") {
		t.Errorf("history assistant message lost its answer: %q", histText)
	}

	// Session store: full text with thinking preserved for the UI.
	var storedAssistant *llm.Message
	for i := range w.messages {
		if w.messages[i].Role == llm.RoleAssistant {
			storedAssistant = &w.messages[i]
		}
	}
	if storedAssistant == nil {
		t.Fatal("no assistant message persisted")
	}
	storeText := storedAssistant.Content
	for _, p := range storedAssistant.Parts {
		storeText += p.Text
	}
	if !strings.Contains(storeText, "<thinking>") || !strings.Contains(storeText, "let me reason") {
		t.Errorf("stored assistant message must keep thinking for UI, got: %q", storeText)
	}
}

// TestLoadConversation_StripsThinking proves resumed assistant turns are
// stripped so the live prefix stays consistent with fresh turns.
func TestLoadConversation_StripsThinking(t *testing.T) {
	prov := makeScriptedProvider("x")
	loop, _ := NewLoop(LoopConfig{Provider: prov, Registry: emptyRegistry()})
	loop.LoadConversation([]llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleAssistant, Content: "<thinking>secret</thinking>visible reply"},
	})
	for _, m := range loop.Messages {
		if m.Role == llm.RoleAssistant && strings.Contains(m.Content, "secret") {
			t.Errorf("resumed assistant message still carries thinking: %q", m.Content)
		}
	}
}
