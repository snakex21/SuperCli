package tui

// audit-130: when a run starts the busy row ("working…") appears. Paths that
// refit the viewport *before* flipping busy leave the frame one row taller
// than the terminal until the next refresh, pushing the header off-screen.

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/storage/session"
)

func audit130Type(m Model, text string) Model {
	for _, r := range text {
		out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = out.(Model)
	}
	return m
}

func TestAudit130SlashCommandBusyRowFitsTerminal(t *testing.T) {
	dir := t.TempDir()
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	m := New(Options{Home: dir, DataDir: dir, Language: "pl",
		StatusFn: func() string { return "status" },
		Commands: map[string]SlashHandler{"auditslow": func(ctx context.Context, _ string) (string, error) {
			<-block
			return "ok", nil
		}}})
	out, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = out.(Model)
	m = audit130Type(m, "/auditslow")
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = out.(Model)
	if !m.busy {
		t.Fatalf("command did not start (busy=false)")
	}
	lines := strings.Split(m.View(), "\n")
	if len(lines) > 24 {
		t.Fatalf("frame is %d rows on a 24-row terminal right after a slash command started (header scrolls off)", len(lines))
	}
}

// Control: a normal resize while busy fits (so the problem is the ordering).
func TestAudit130BusyAfterRefreshFits(t *testing.T) {
	dir := t.TempDir()
	m := New(Options{Home: dir, DataDir: dir, Language: "pl", StatusFn: func() string { return "status" }})
	out, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = out.(Model)
	m.busy = true
	m.refreshTranscript()
	if n := len(strings.Split(m.View(), "\n")); n > 24 {
		t.Fatalf("busy frame %d rows > 24", n)
	}
}

// /resume flips busy while the session loads; the viewport must be refit for
// the working row at once, not one row too tall until the next refresh.
func TestResumeBusyRowRefitsViewport(t *testing.T) {
	dir := t.TempDir()
	store, err := session.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	m := New(Options{Home: dir, DataDir: dir, Language: "pl", StatusFn: func() string { return "status" }})
	m.sessionStore = store
	out, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = out.(Model)
	for i := 0; i < 60; i++ {
		m.appendLine("line")
	}
	m.refreshTranscript()
	out, _ = m.resumeConversation("missing-session")
	m = out.(Model)
	if !m.busy {
		t.Fatal("resume did not start (busy=false)")
	}
	got := m.viewport.Height
	refit := m
	refit.refreshTranscript()
	if got != refit.viewport.Height {
		t.Fatalf("viewport height %d right after /resume started, %d after a refit (busy row not accounted for)", got, refit.viewport.Height)
	}
	if n := len(strings.Split(m.View(), "\n")); n > 24 {
		t.Fatalf("frame is %d rows on a 24-row terminal", n)
	}
}
