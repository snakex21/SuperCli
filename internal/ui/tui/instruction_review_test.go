package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestInstructionsCompactListShowsEnableState(t *testing.T) {
	for _, language := range []string{"en", "pl"} {
		m := instructionsOpen(instructionsTestModel(t))
		m.language = language
		m = instructionsCreate(m, "Preset", "content")
		m.width, m.height = 24, 14
		for _, enabled := range []bool{false, true, false} {
			view := ansi.Strip(m.renderMenuView())
			status := m.tr("tui.instructions.disabled")
			if enabled {
				status = m.tr("tui.instructions.enabled")
			}
			if m.menu.instructions.Enabled != enabled || !strings.Contains(view, status) {
				t.Fatalf("%s enabled=%v: status %q missing from compact list:\n%s", language, enabled, status, view)
			}
			if len(strings.Split(view, "\n")) > m.height {
				t.Fatal("list exceeds terminal height")
			}
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > m.width {
					t.Fatalf("list exceeds terminal width: %q", line)
				}
			}
			m = instructionsKey(m, tea.KeyMsg{Type: tea.KeySpace})
		}
	}
}

func instructionCaret(t *testing.T, b instructionBuffer, width int) (int, int) {
	t.Helper()
	_, row, column := b.visualRows(width)
	return row, column
}

func assertInstructionCaretVisible(t *testing.T, b instructionBuffer, width, height int) {
	t.Helper()
	// Give the caret a unique visible style; count exactly one rendered caret.
	renderer := lipgloss.NewRenderer(nil)
	renderer.SetColorProfile(termenv.TrueColor)
	palette := Palette{InputText: renderer.NewStyle()}
	view := b.view(width, height, true, palette)
	if !strings.Contains(view, "\x1b[7m") {
		t.Fatalf("caret missing from viewport: %q", view)
	}
	if got := len(strings.Split(view, "\n")); got != height {
		t.Fatalf("viewport height = %d, want %d", got, height)
	}
	for _, line := range strings.Split(view, "\n") {
		if ansi.StringWidth(line) > width {
			t.Fatalf("viewport line exceeds %d cells: %q", width, line)
		}
	}
}

func TestInstructionBufferVisualNavigationUnicodeParagraph(t *testing.T) {
	for _, width := range []int{8, 11, 20} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			content := strings.Repeat("日a本ę語z ", 100)
			b := newInstructionBuffer(content)
			b.update(tea.KeyMsg{Type: tea.KeyCtrlHome}, true, width, 3)
			b.update(tea.KeyMsg{Type: tea.KeyRight}, true, width, 3) // after a double-width rune
			start := b.at
			row, column := instructionCaret(t, b, width)
			if row != 0 || column != 2 {
				t.Fatal("unexpected starting caret")
			}
			for i := 1; i <= 12; i++ {
				b.update(tea.KeyMsg{Type: tea.KeyDown}, true, width, 3)
				got, col := instructionCaret(t, b, width)
				if got != i || col > column || column-col > 1 {
					t.Fatalf("down: row/column = %d/%d; want row %d near column %d", got, col, i, column)
				}
				assertInstructionCaretVisible(t, b, width, 3)
			}
			for i := 11; i >= 0; i-- {
				b.update(tea.KeyMsg{Type: tea.KeyUp}, true, width, 3)
				got, _ := instructionCaret(t, b, width)
				if got != i {
					t.Fatalf("up: row = %d, want %d", got, i)
				}
				assertInstructionCaretVisible(t, b, width, 3)
			}
			if b.at != start {
				t.Fatal("visual up/down lost preferred display column")
			}
			b.update(tea.KeyMsg{Type: tea.KeyPgDown}, true, width, 3)
			row, _ = instructionCaret(t, b, width)
			if row != 3 {
				t.Fatalf("PgDown moved to row %d, want 3", row)
			}
			assertInstructionCaretVisible(t, b, width, 3)
			b.update(tea.KeyMsg{Type: tea.KeyPgUp}, true, width, 3)
			if b.at != start {
				t.Fatal("PgUp did not return to starting caret")
			}
			b.update(tea.KeyMsg{Type: tea.KeyCtrlEnd}, true, width, 3)
			endRow, _ := instructionCaret(t, b, width)
			b.update(tea.KeyMsg{Type: tea.KeyDown}, true, width, 3)
			if b.at != len([]rune(content)) {
				t.Fatal("down at EOF moved caret")
			}
			b.update(tea.KeyMsg{Type: tea.KeyPgUp}, true, width, 3)
			row, _ = instructionCaret(t, b, width)
			if row != endRow-3 {
				t.Fatal("PgUp from EOF did not move visual rows")
			}
			assertInstructionCaretVisible(t, b, width, 3)
			if b.value() != content {
				t.Fatal("navigation modified content")
			}
		})
	}
}

func TestInstructionsEditorUsesRenderedViewportForNavigation(t *testing.T) {
	content := strings.Repeat("日本語aę ", 1000)
	for _, size := range [][2]int{{24, 14}, {40, 20}, {160, 32}} {
		m := instructionsOpen(instructionsTestModel(t))
		m = instructionsCreate(m, "Preset", content)
		m = instructionsType(m, "e")
		m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyTab})
		m.width, m.height = size[0], size[1]
		width, height := instructionEditorSize(minInt(m.width, 120)-4, m.height-4)
		m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyCtrlHome})
		m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyDown})
		row, _ := instructionCaret(t, m.menu.instructionEditor.content, width)
		if row != 1 {
			t.Fatalf("size %v: Down moved to row %d", size, row)
		}
		m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyPgDown})
		row, _ = instructionCaret(t, m.menu.instructionEditor.content, width)
		if row != 1+height {
			t.Fatalf("size %v: PgDown moved to row %d, want %d", size, row, 1+height)
		}
		assertInstructionCaretVisible(t, m.menu.instructionEditor.content, width, height)
		m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyUp})
		m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyPgUp})
		row, _ = instructionCaret(t, m.menu.instructionEditor.content, width)
		if row != 0 {
			t.Fatalf("size %v: navigation did not return to top", size)
		}
		m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyCtrlS})
		if instructionsLoad(t, m).Presets[0].Content != content {
			t.Fatal("navigation/save changed paragraph")
		}
	}
}
