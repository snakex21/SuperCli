package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"supercli/internal/agent"
	"testing"
)

func wheel(button tea.MouseButton, x, y int) tea.MouseMsg {
	return tea.MouseMsg{Button: button, Action: tea.MouseActionPress, X: x, Y: y}
}

func TestMouseWheelScrollsIdleAndStreamingWithoutChangingDraft(t *testing.T) {
	for _, busy := range []bool{false, true} {
		m := streamTestModel(t)
		m.busy = busy
		m.chat.addAssistant(strings.Repeat("history line\n", 100))
		m.input.SetValue("unsent draft")
		m.refreshTranscript()
		m.viewport.GotoBottom()
		bottom := m.viewport.YOffset
		next, cmd := m.Update(wheel(tea.MouseButtonWheelUp, 10, 5))
		m = next.(Model)
		if cmd != nil || m.viewport.YOffset >= bottom || m.input.Value() != "unsent draft" {
			t.Fatalf("busy=%v wheel changed draft or failed to scroll", busy)
		}
		offset := m.viewport.YOffset
		next, _ = m.Update(runEventMsg{ev: agent.MessageEvent{Text: "new streamed text"}})
		m = next.(Model)
		if m.viewport.YOffset != offset {
			t.Fatal("stream stole mouse scroll position")
		}
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
		m = next.(Model)
		if !m.viewport.AtBottom() {
			t.Fatal("End did not restore follow")
		}
	}
}

func TestMouseWheelAtBottomRestoresFollow(t *testing.T) {
	m := streamTestModel(t)
	m.chat.addAssistant(strings.Repeat("history line\n", 100))
	m.refreshTranscript()
	m.viewport.GotoBottom()
	next, _ := m.Update(wheel(tea.MouseButtonWheelUp, 8, 4))
	m = next.(Model)
	next, _ = m.Update(wheel(tea.MouseButtonWheelDown, 8, 4))
	m = next.(Model)
	if !m.viewport.AtBottom() {
		t.Fatal("wheel down did not return to bottom")
	}
	next, _ = m.Update(runEventMsg{ev: agent.MessageEvent{Text: "latest"}})
	m = next.(Model)
	if !m.viewport.AtBottom() || !strings.Contains(m.viewport.View(), "latest") {
		t.Fatal("tail did not follow after wheel down")
	}
}

func TestMouseDoesNotScrollThroughModalOrComposer(t *testing.T) {
	m := streamTestModel(t)
	m.chat.addAssistant(strings.Repeat("history line\n", 100))
	m.refreshTranscript()
	m.viewport.GotoBottom()
	before := m.viewport.YOffset
	for _, event := range []tea.MouseMsg{
		wheel(tea.MouseButtonWheelUp, 5, 0),
		wheel(tea.MouseButtonWheelUp, 5, m.height-2),
		wheel(tea.MouseButtonWheelUp, -1, 4),
		{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: 5, Y: 4},
	} {
		next, _ := m.Update(event)
		m = next.(Model)
		if m.viewport.YOffset != before {
			t.Fatal("mouse outside transcript changed scroll")
		}
	}
	m.enterMenu(interactiveMenu{kind: menuActions})
	next, _ := m.Update(wheel(tea.MouseButtonWheelUp, 5, 4))
	m = next.(Model)
	if m.viewport.YOffset != before || m.mode != modeMenu {
		t.Fatal("mouse changed hidden transcript or dismissed modal")
	}
}

func TestAnswerBoundaryKeepsExplicitFinalTextVisible(t *testing.T) {
	for _, lang := range []string{"pl", "en"} {
		label := textFor(lang, "Answer", "Odpowiedź")
		for _, collapsed := range []bool{false, true} {
			out := renderAssistantMarkdown("<thinking>private plan</thinking>\nI think this is the answer.\n\nMore detail.", NoColorPalette(), collapsed, lang)
			if strings.Count(out, "── "+label+" ──") != 1 {
				t.Fatal(out)
			}
			if !strings.Contains(out, "I think this is the answer.") || !strings.Contains(out, "More detail.") {
				t.Fatal("answer reclassified as thinking: " + out)
			}
			if strings.Index(out, label) > strings.Index(out, "I think this is the answer.") {
				t.Fatal(out)
			}
			if collapsed && strings.Contains(out, "private plan") {
				t.Fatal("folded reasoning visible")
			}
			if !collapsed && strings.Index(out, "private plan") > strings.Index(out, label) {
				t.Fatal(out)
			}
		}
	}
}

func TestAnswerBoundaryWaitsForVisibleAnswer(t *testing.T) {
	for _, input := range []string{"<thinking>still working", "<thinking>finished</thinking>\n\n", "<thinking></thinking>", "<thinking> \n </thinking>"} {
		if out := renderAssistantMarkdown(input, NoColorPalette(), false, "pl"); strings.Contains(out, "Odpowiedź") {
			t.Fatal(out)
		}
	}
	out := renderAssistantMarkdown("Normal answer", NoColorPalette(), false, "en")
	if strings.Contains(out, "Thinking") || strings.Contains(out, "── Answer") {
		t.Fatal(out)
	}
}
