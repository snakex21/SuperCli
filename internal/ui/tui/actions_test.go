package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"supercli/internal/storage/session"
)

func TestActionsMenuOpensWithoutSlash(t *testing.T) {
	m := New(Options{Home: t.TempDir(), Language: "pl"})
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	mm := out.(Model)
	if cmd != nil {
		t.Fatal("opening actions should be local")
	}
	if mm.mode != modeMenu || mm.menu.kind != menuActions {
		t.Fatalf("mode=%v kind=%v, want actions menu", mm.mode, mm.menu.kind)
	}
	view := mm.View()
	for _, want := range []string{"Centrum działań", "Wybierz model", "Ostatnie sesje", "Ustawienia"} {
		if !strings.Contains(view, want) {
			t.Fatalf("actions view missing %q: %q", want, view)
		}
	}
}

func TestWelcomeFitsNarrowTerminal(t *testing.T) {
	view := welcomeAtWidth(Options{}, NoColorPalette(), 80)
	for i, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got > 80 {
			t.Fatalf("line %d width=%d exceeds terminal: %q", i+1, got, line)
		}
	}
}

func TestWelcomeUsesCompactLayoutInShortTerminal(t *testing.T) {
	view := welcomeAtSize(Options{}, NoColorPalette(), 80, 22)
	lines := strings.Split(strings.TrimSuffix(view, "\n"), "\n")
	if len(lines) > 5 {
		t.Fatalf("compact welcome uses %d lines, want at most 5:\n%s", len(lines), view)
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got > 80 {
			t.Fatalf("line %d width=%d exceeds terminal: %q", i+1, got, line)
		}
	}
}

func TestActionsMenuFiltersAndOpensExistingScreen(t *testing.T) {
	m := New(Options{Home: t.TempDir()})
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	mm := out.(Model)
	for _, r := range []rune("model") {
		out, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		mm = out.(Model)
	}
	if rows := mm.filteredActionRows(); len(rows) == 0 || rows[0].id != "model" {
		t.Fatalf("filtered actions = %+v", rows)
	}
	out, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm = out.(Model)
	if mm.menu.kind != menuModels {
		t.Fatalf("kind=%v, want models", mm.menu.kind)
	}
}

func TestActionsTabDoesNotStealNonEmptyInput(t *testing.T) {
	m := New(Options{Home: t.TempDir()})
	m.input.SetValue("draft")
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	mm := out.(Model)
	if mm.mode == modeMenu {
		t.Fatal("Tab with a draft must not open actions")
	}
}

func TestTranscriptSearchOpensWithoutSlashAndJumps(t *testing.T) {
	m := New(Options{Home: t.TempDir(), NoColor: true})
	m.width, m.height = 80, 24
	m.chat.addUser("first question")
	m.chat.addSystem("tool output\nsecond line")
	m.chat.addAssistant("needle answer")
	m.refreshTranscript()
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	mm := out.(Model)
	if mm.mode != modeMenu || mm.menu.kind != menuTranscript {
		t.Fatalf("mode=%v kind=%v", mm.mode, mm.menu.kind)
	}
	for _, r := range []rune("needle") {
		out, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		mm = out.(Model)
	}
	if got := len(mm.filteredTranscriptRows()); got != 1 {
		t.Fatalf("matches=%d", got)
	}
	out, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm = out.(Model)
	if mm.mode != modeNormal {
		t.Fatalf("mode=%v, want normal", mm.mode)
	}
}

func TestSessionsMenuSelectsWithoutSlashBubble(t *testing.T) {
	home := t.TempDir()
	store, err := session.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	old, err := store.Create(home, "qwen", "Poprzednia rozmowa")
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.Create(home, "qwen", "Bieżąca rozmowa")
	if err != nil {
		t.Fatal(err)
	}

	var resumed string
	m := New(Options{
		Home:         home,
		SessionID:    current.ID,
		SessionStore: store,
		Commands: map[string]SlashHandler{
			"resume": func(_ context.Context, args string) (string, error) {
				resumed = args
				return "resumed", nil
			},
		},
	})
	out, _ := m.openSessionsMenu()
	mm := out.(Model)
	if len(mm.menu.sessions) != 1 || mm.menu.sessions[0].ID != old.ID {
		t.Fatalf("sessions = %+v", mm.menu.sessions)
	}
	out, cmd := mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm = out.(Model)
	if cmd == nil {
		t.Fatal("session selection should dispatch resume")
	}
	msg := cmd()
	if resumed != old.ID {
		t.Fatalf("resumed=%q want %q", resumed, old.ID)
	}
	if _, ok := msg.(slashResultMsg); !ok {
		t.Fatalf("message=%T, want slashResultMsg", msg)
	}
	if strings.Contains(mm.transcript.String(), "/resume") {
		t.Fatalf("visual selection leaked slash command: %q", mm.transcript.String())
	}
}

func TestIntentMenusFitNarrowTerminal(t *testing.T) {
	m := New(Options{Home: t.TempDir(), NoColor: true})
	m.width, m.height = 48, 18
	m.mode = modeMenu
	m.menu = interactiveMenu{kind: menuActions}
	assertLinesFit(t, m.renderActionsMenu(), m.width)

	m.menu = interactiveMenu{kind: menuSessions, sessions: []session.Session{{
		Title: "A deliberately long conversation title that must wrap compactly",
		Model: "provider/a-very-long-model-name", Provider: "provider", MessageCount: 123,
	}}}
	assertLinesFit(t, m.renderSessionsMenu(), m.width)
}

func TestPrimaryMenusFitCommonTerminalSizes(t *testing.T) {
	kinds := []menuKind{menuActions, menuSessions, menuModels, menuModelCatalog, menuProviders, menuProjects, menuGoal, menuReasoning, menuSettings}
	for _, width := range []int{48, 64, 80, 120} {
		for _, height := range []int{16, 24, 40} {
			for _, kind := range kinds {
				m := New(Options{Home: t.TempDir(), NoColor: true})
				m.width, m.height, m.mode = width, height, modeMenu
				m.menu = interactiveMenu{kind: kind}
				assertLinesFit(t, m.renderMenuView(), width)
			}
		}
	}
}

func TestCheckpointPreviewConfirmsBeforeUndo(t *testing.T) {
	called := false
	m := New(Options{
		Home: t.TempDir(), NoColor: true,
		CheckpointPreview: func(redo bool) (CheckpointPreview, error) {
			return CheckpointPreview{ID: "turn-7", Prompt: "change config", Files: []string{"config.toml", "internal/app/main.go"}}, nil
		},
		CheckpointUndo: func(context.Context, bool) (string, error) {
			called = true
			return "reverted", nil
		},
	})
	out, cmd := m.openCheckpointMenu(false)
	mm := out.(Model)
	if cmd != nil || called {
		t.Fatal("preview must not change files")
	}
	if view := mm.View(); !strings.Contains(view, "turn-7") || !strings.Contains(view, "config.toml") {
		t.Fatalf("preview missing checkpoint scope: %q", view)
	}
	out, cmd = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = out.(Model)
	if cmd == nil {
		t.Fatal("confirmation should dispatch undo")
	}
	_ = cmd()
	if !called {
		t.Fatal("undo not called after confirmation")
	}
}

func assertLinesFit(t *testing.T, view string, width int) {
	t.Helper()
	for i, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line %d width=%d exceeds %d: %q", i+1, got, width, line)
		}
	}
}
