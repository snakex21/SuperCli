package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"supercli/internal/llm"
)

func TestTranscriptReplacementPreservesVisibleHistorySearchAndCopy(t *testing.T) {
	m := New(Options{NoColor: true, Language: "en"})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 36})
	m = next.(Model)
	welcome := m.viewport.View()
	h := &resumedTranscript{ID: "fixture", Seqs: []int{1, 2, 3, 4}, Messages: []llm.Message{
		{Role: llm.RoleUser, Content: "question needle"},
		{Role: llm.RoleAssistant, Parts: []llm.ContentPart{{Type: llm.PartTypeReasoning, Text: "plan"}, {Type: llm.PartTypeText, Text: "answer needle"}}},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call", Name: "read_file", Arguments: "{}"}}},
		{Role: llm.RoleTool, ToolCallID: "call", Content: "output needle"},
	}}
	m.applyResumedTranscript(h)
	answer := m.chat.lastAssistant()
	if !strings.Contains(answer, "answer needle") || len(m.chat.search("needle")) != 3 {
		t.Fatal("resume lost raw answer or searchable canonical messages")
	}
	old := m
	next, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 36})
	m = next.(Model)
	if !strings.Contains(ansi.Strip(m.viewport.View()), "answer needle") {
		t.Fatal("resize replaced resumed conversation with welcome")
	}
	m.applyResumedTranscript(&resumedTranscript{ID: "replacement", Seqs: []int{1, 2}, Messages: []llm.Message{
		{Role: llm.RoleUser, Content: "replacement question"},
		{Role: llm.RoleAssistant, Content: "replacement exact answer"},
	}})
	if old.chat.lastAssistant() != answer || m.chat.lastAssistant() != "replacement exact answer" || len(m.chat.search("needle")) != 0 {
		t.Fatal("session replacement retained old search results or changed copied answer")
	}
	m.applyResumedTranscript(&resumedTranscript{ID: "empty"})
	next, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 36})
	m = next.(Model)
	if m.viewport.View() != welcome || m.chat.lastAssistant() != "" {
		t.Fatal("empty conversation did not restore welcome and clear copied answer")
	}
}

func TestEmptyTranscriptAppendPreservesPresenceAcrossResize(t *testing.T) {
	m := New(Options{NoColor: true, Language: "en"})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 36})
	m = next.(Model)
	m.appendLine("") // An empty system line still starts a conversation.
	m.refreshTranscript()
	next, _ = m.Update(tea.WindowSizeMsg{Width: 81, Height: 37})
	m = next.(Model)
	if len(m.chat.msgs) != 1 || m.completedLines() != "\n" || strings.TrimSpace(ansi.Strip(m.viewport.View())) != "" {
		t.Fatal("empty appended line was replaced by a welcome screen")
	}
}
