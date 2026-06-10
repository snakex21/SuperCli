package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNormalizePastedText_KeepsNewlines(t *testing.T) {
	in := "func main() {\r\n\tfmt.Println(\"hi\")\r\n}\r\n"
	got := normalizePastedText(in)
	want := "func main() {\n\tfmt.Println(\"hi\")\n}"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNormalizePastedText_StripsControlChars(t *testing.T) {
	in := "a\x00b\x1bc\nd\te"
	got := normalizePastedText(in)
	if got != "abc\nd\te" {
		t.Errorf("got %q", got)
	}
}

func TestNormalizePastedText_BareCRBecomesNewline(t *testing.T) {
	if got := normalizePastedText("a\rb"); got != "a\nb" {
		t.Errorf("got %q", got)
	}
}

func TestNormalizePastedLine_CollapsesNewlines(t *testing.T) {
	if got := normalizePastedLine("a\r\nb\nc"); got != "a b c" {
		t.Errorf("got %q", got)
	}
}

func TestAltEnterInsertsNewline(t *testing.T) {
	m := newTestModel(t)
	m.input.SetValue("line1")
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	m2 := mm.(Model)
	if got := m2.input.Value(); got != "line1\n" {
		t.Fatalf("alt+enter should insert newline, got %q", got)
	}
	if m2.input.Height() != 2 {
		t.Errorf("input height = %d, want 2 after newline", m2.input.Height())
	}
}

func TestCtrlJInsertsNewline(t *testing.T) {
	m := newTestModel(t)
	m.input.SetValue("x")
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	m2 := mm.(Model)
	if got := m2.input.Value(); got != "x\n" {
		t.Fatalf("ctrl+j should insert newline, got %q", got)
	}
}

func TestPlainEnterDoesNotInsertNewline(t *testing.T) {
	m := newTestModel(t)
	m.input.SetValue("hello")
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m2 := mm.(Model)
	// No agent wired: enter consumes the input (send path),
	// it must NOT add a newline.
	if strings.Contains(m2.input.Value(), "\n") {
		t.Fatalf("plain enter must not insert newline, got %q", m2.input.Value())
	}
}

func TestInputHeightClampedToMax(t *testing.T) {
	m := newTestModel(t)
	m.input.SetValue(strings.Repeat("line\n", 9) + "last")
	m.syncInputHeight()
	if m.input.Height() != maxInputLines {
		t.Errorf("height = %d, want clamp to %d", m.input.Height(), maxInputLines)
	}
	m.input.Reset()
	m.syncInputHeight()
	if m.input.Height() != 1 {
		t.Errorf("height after reset = %d, want 1", m.input.Height())
	}
}

func TestChatLastAssistant(t *testing.T) {
	c := newChat(80)
	if c.lastAssistant() != "" {
		t.Error("empty chat should return empty string")
	}
	c.addUser("question")
	c.addAssistant("first answer")
	c.addSystem("[marker]")
	c.addAssistant("second answer")
	c.addUser("another question")
	if got := c.lastAssistant(); got != "second answer" {
		t.Errorf("lastAssistant = %q, want %q", got, "second answer")
	}
	c.current = "streaming..."
	if got := c.lastAssistant(); got != "streaming..." {
		t.Errorf("lastAssistant during stream = %q", got)
	}
}

func TestShouldIgnoreAltKey_AllowsAltEnter(t *testing.T) {
	if shouldIgnoreAltKey(tea.KeyMsg{Type: tea.KeyEnter, Alt: true}) {
		t.Error("alt+enter must not be ignored (it inserts a newline)")
	}
	// Alt+letter shortcuts are still ignored.
	if !shouldIgnoreAltKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}, Alt: true}) {
		t.Error("alt+a should still be ignored")
	}
}

func TestHintLineMentionsNewKeys(t *testing.T) {
	m := newTestModel(t)
	hint := m.renderHintLine()
	if !strings.Contains(hint, "Alt+Enter") {
		t.Error("hint line missing Alt+Enter")
	}
	if !strings.Contains(hint, "Ctrl+Y") {
		t.Error("hint line missing Ctrl+Y")
	}
}

func TestHelpContentMentionsNewKeys(t *testing.T) {
	h := HelpContent()
	for _, want := range []string{"Alt+Enter", "Ctrl+Y", "Shift+T", "Shift+E"} {
		if !strings.Contains(h, want) {
			t.Errorf("/help missing %s", want)
		}
	}
}
