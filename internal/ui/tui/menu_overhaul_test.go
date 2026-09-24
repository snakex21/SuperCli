package tui

import (
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"supercli/internal/llm"
)

func TestReasoningConfirmationDoesNotMoveConversation(t *testing.T) {
	t.Cleanup(func() { _ = llm.SetReasoningEffort("") })
	p, _ := newStubLLM("gpt-5.5")
	m := New(Options{Home: t.TempDir(), DataDir: t.TempDir(), LLM: p, NoColor: true})
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = out.(Model)
	m.input.SetValue("draft stays here")
	original := m.View()
	next, _ := m.openReasoningMenu()
	m = next.(Model)
	m.menu.cursor = m.reasoningOptionIndex("xhigh")
	next, cmd := m.selectReasoningEffort()
	m = next.(Model)
	if cmd == nil || !m.statusSuccess || !strings.Contains(m.statusOverride, "xhigh") {
		t.Fatal("successful change needs a success confirmation")
	}
	if m.input.Value() != "draft stays here" || !strings.Contains(m.renderHeader(), "xhigh") {
		t.Fatal("reasoning change lost draft or failed to refresh header")
	}
	before := strings.Split(original, "\n")
	after := strings.Split(m.View(), "\n")
	if len(before) != len(after) {
		t.Fatalf("notice moved input: %d -> %d rows", len(before), len(after))
	}
	for i := 1; i < len(before)-1; i++ {
		if before[i] != after[i] {
			t.Fatalf("confirmation changed chat/input row %d", i)
		}
	}
	next, _ = m.Update(statusOverrideClearMsg{revision: m.statusRevision})
	m = next.(Model)
	if len(strings.Split(m.View(), "\n")) != len(before) {
		t.Fatal("expiring notice moved the composer")
	}
}

func TestOlderNoticeCannotClearNewerNotice(t *testing.T) {
	m := New(Options{NoColor: true})
	m.setStatus("saved", true)
	old := statusOverrideClearMsg{revision: m.statusRevision}
	m.setStatus("save failed", false)
	next, _ := m.Update(old)
	m = next.(Model)
	if m.statusOverride != "save failed" || m.statusSuccess {
		t.Fatal("old timer cleared or recolored the newer error")
	}
}

func TestReasoningReturnsToParentAndPreservesSearch(t *testing.T) {
	t.Cleanup(func() { _ = llm.SetReasoningEffort("") })
	p, _ := newStubLLM("gpt-5.5")
	m := New(Options{Home: t.TempDir(), DataDir: t.TempDir(), LLM: p, NoColor: true})
	m.enterMenu(interactiveMenu{kind: menuActions, filter: "reasoning"})
	next, _ := m.openReasoningMenu()
	m = next.(Model)
	m.menu.cursor = m.reasoningOptionIndex("high")
	next, _ = m.selectReasoningEffort()
	m = next.(Model)
	if m.menu.kind != menuActions || m.menu.filter != "reasoning" {
		t.Fatal("applying reasoning lost the parent menu")
	}
}

func TestProviderKeyMaskIsIndependentOfLanguage(t *testing.T) {
	for _, language := range []string{"pl", "en"} {
		m := New(Options{NoColor: true, Language: language})
		m.enterMenu(interactiveMenu{kind: menuProviderForm, form: []string{"local", "openai", "http://localhost", "test-secret-key", "qwen"}, formAt: 3})
		if strings.Contains(m.View(), "test-secret-key") {
			t.Fatal("translated API key field exposed a credential")
		}
		next, _ := m.handleFormKey(tea.KeyMsg{Type: tea.KeyRight})
		m = next.(Model)
		if !strings.Contains(m.View(), "test-secret-key") {
			t.Fatal("explicit reveal does not work")
		}
		next, _ = m.handleFormKey(tea.KeyMsg{Type: tea.KeyDown})
		m = next.(Model)
		if strings.Contains(m.View(), "test-secret-key") {
			t.Fatal("leaving the field should hide the key again")
		}
	}
}

func TestMenuLayoutKeepsSelectionAndErrorsVisible(t *testing.T) {
	renderer := lipgloss.NewRenderer(io.Discard, termenv.WithProfile(termenv.TrueColor))
	renderer.SetColorProfile(termenv.TrueColor)
	renderer.SetHasDarkBackground(true)
	for _, language := range []string{"pl", "en"} {
		for _, size := range [][2]int{{32, 10}, {48, 12}, {80, 24}, {120, 35}, {160, 45}} {
			for _, colored := range []bool{false, true} {
				m := New(Options{NoColor: true, Language: language})
				if colored {
					m.palette = NewPalette(renderer)
				}
				m.width, m.height = size[0], size[1]
				m.enterMenu(interactiveMenu{kind: menuActions})
				m.menu.cursor = len(m.filteredActionRows()) - 1
				view := m.View()
				assertLinesFit(t, view, m.width)
				if lipgloss.Height(view) != m.height {
					t.Fatalf("unstable frame at %v", size)
				}
				if !strings.Contains(ansi.Strip(view), "› ") {
					t.Fatal("selected row disappeared below the frame")
				}
				if colored && !strings.Contains(view, "\x1b") {
					t.Fatal("color test did not exercise ANSI rendering")
				}
				if !colored && strings.Contains(view, "\x1b") {
					t.Fatal("--no-color emitted ANSI")
				}
				m.enterMenu(interactiveMenu{kind: menuContextLimit, cursor: len(contextMenuValues) - 1, editing: true, editBuf: "invalid", formErr: "Invalid context value"})
				view = m.View()
				if !strings.Contains(view, "Invalid context value") || !strings.Contains(view, "invalid") {
					t.Fatalf("short terminal lost edited field/error at %v", size)
				}
				assertLinesFit(t, view, m.width)
			}
		}
	}
}

func TestSettingsTabsDoNotChangeWhileEditing(t *testing.T) {
	m := cursorForKey(newSettingsModel(t, ""), "task_model")
	next, _ := m.settingsEnter()
	m = next.(Model)
	category := m.menu.category
	next, _ = m.handleMenuKey(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(Model)
	if m.menu.category != category || !m.menu.editing {
		t.Fatal("arrow changed category during value editing")
	}
	next, _ = m.handleMenuKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	next, _ = m.handleMenuKey(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(Model)
	if m.menu.category == category || m.menu.cursor != 0 {
		t.Fatal("category navigation not restored after cancelling edit")
	}
}

func TestVisibleTruncationPreservesANSIAndWideCharacters(t *testing.T) {
	input := "\x1b[38;5;209mZażółć 日本語 👋\x1b[0m"
	for width := 1; width < 25; width++ {
		out := truncateVisible(input, width)
		if lipgloss.Width(out) > width {
			t.Fatalf("width %d overflowed", width)
		}
		if strings.Contains(ansi.Strip(out), "\x1b") {
			t.Fatal("truncation left an incomplete ANSI sequence")
		}
	}
}

func TestStatusBarTruncatesVisibleColumnsWithoutCuttingANSI(t *testing.T) {
	renderer := lipgloss.NewRenderer(io.Discard)
	renderer.SetColorProfile(termenv.TrueColor)
	renderer.SetHasDarkBackground(true)
	p := NewPalette(renderer)
	plain := NoColorPalette()
	for _, width := range []int{12, 24, 80, 120} {
		bar := StatusBar{Model: "gpt-5.5 (xhigh)", Tokens: "12.5k", Session: "session", Width: width}
		colored := bar.Render(p)
		if !strings.Contains(colored, "\x1b") {
			t.Fatal("expected colored status")
		}
		if ansi.Strip(colored) != bar.Render(plain) {
			t.Fatalf("color escapes changed visible status at width %d", width)
		}
	}
}
