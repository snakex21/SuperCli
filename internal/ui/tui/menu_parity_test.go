package tui

import (
	"context"
	tea "github.com/charmbracelet/bubbletea"
	"os"
	"path/filepath"
	"strings"
	"supercli/internal/storage"
	"supercli/internal/storage/goal"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
	"testing"
	"time"
)

func goalAction(t *testing.T, m Model, id string) Model {
	t.Helper()
	for i, row := range m.goalMenuRows() {
		if row.id == id {
			m.menu.cursor = i
			return navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
		}
	}
	t.Fatalf("missing goal action %s", id)
	return m
}
func TestGoalMenuCreatesNamedStepsPausesResumesAndRequiresVerification(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	gs := goal.NewStorage(db)
	if err := gs.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	svc := goal.NewService(gs)
	m := New(Options{Home: dir, DataDir: dir, GoalService: svc, Language: "pl", NoColor: true})
	next, _ := m.openGoalMenu()
	m = next.(Model)
	m = goalAction(t, m, "new")
	for _, value := range []string{"Napraw wyszukiwanie", "Repo projektu", "Wyszukiwanie trafia w potrzebny kod"} {
		m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)})
		m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	}
	if g := svc.Active(); g == nil || g.Title != "Napraw wyszukiwanie" || g.SuccessCriteria == "" {
		t.Fatalf("goal not saved: %+v", g)
	}
	id := svc.Active().ID
	m = goalAction(t, m, "task")
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Sprawdź trafienia symboli")})
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	tasks, _ := svc.ListTasks(context.Background(), "")
	if len(tasks) != 1 || tasks[0].Title != "Sprawdź trafienia symboli" {
		t.Fatalf("tasks: %+v", tasks)
	}
	m = goalAction(t, m, "done")
	if svc.Active() == nil || m.menu.formErr == "" {
		t.Fatal("unverified/incomplete goal was accepted")
	}
	m = goalAction(t, m, "pause")
	if svc.Active() != nil {
		t.Fatal("goal not paused")
	}
	m = goalAction(t, m, "resume")
	if svc.Active() == nil || svc.Active().ID != id {
		t.Fatal("paused goal not resumed")
	}
	m = goalAction(t, m, "toggle")
	m = goalAction(t, m, "verify")
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Test trafień symboli przeszedł")})
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = goalAction(t, m, "done")
	if svc.Active() != nil || m.menu.formErr != "" {
		t.Fatalf("verified goal not completed: %q", m.menu.formErr)
	}
	saved, err := gs.GetGoal(context.Background(), id)
	if err != nil || saved.Status != goal.StatusDone {
		t.Fatalf("saved goal: %+v %v", saved, err)
	}
}
func TestContextMenuUsesScopedBudgetWithoutSendingPrompt(t *testing.T) {
	dir := t.TempDir()
	store := config.LoadModelContextStore(dir)
	m := New(Options{Home: dir, DataDir: dir, ModelSwapper: &testModelSwapper{current: "qwen"}, ActiveProvider: "local", ModelContextStore: store})
	m.input.SetValue("draft")
	next, _ := m.openContextLimitMenu()
	m = next.(Model)
	for i := 0; i < 3; i++ {
		m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("missing confirmation")
	}
	_ = cmd()
	if tokens, ok := store.Get("local", "qwen"); !ok || tokens != 100000 {
		t.Fatalf("budget: %d %v", tokens, ok)
	}
	if m.busy || m.input.Value() != "draft" {
		t.Fatal("context selection sent/lost a draft")
	}
	next, _ = m.openContextLimitMenu()
	m = next.(Model)
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEnd})
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("invalid")})
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.menu.editing || m.menu.formErr == "" {
		t.Fatal("invalid custom budget silently accepted")
	}
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.menu.kind != menuContextLimit || m.menu.editing {
		t.Fatal("Esc should cancel custom edit first")
	}
}
func TestSessionDatesUseSelectedLanguage(t *testing.T) {
	for _, tc := range []struct{ lang, today, yesterday string }{{"en", "today ", "yesterday "}, {"pl", "dzisiaj ", "wczoraj "}} {
		m := New(Options{Language: tc.lang})
		now := time.Now()
		if !strings.HasPrefix(m.relativeSessionTime(now), tc.today) || !strings.HasPrefix(m.relativeSessionTime(now.AddDate(0, 0, -1)), tc.yesterday) {
			t.Fatalf("date labels do not use %s", tc.lang)
		}
	}
}
func TestExportMenuUsesActiveSessionAndPortableDataDirectory(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	store, err := session.OpenStore(data)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	current, err := store.Create(home, "qwen", "chosen session")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(home, "other", "newer unrelated session"); err != nil {
		t.Fatal(err)
	}
	m := New(Options{Home: home, DataDir: data, SessionID: current.ID, SessionStore: store})
	_, cmd := m.dispatchVisualCommand("export", "chosen.md")
	if msg := cmd().(slashResultMsg); msg.Err != nil {
		t.Fatal(msg.Err)
	}
	body, err := os.ReadFile(filepath.Join(data, "chosen.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "chosen session") || strings.Contains(string(body), "newer unrelated session") {
		t.Fatal("export contains the wrong session")
	}
	if _, err := os.Stat(filepath.Join(home, "chosen.md")); !os.IsNotExist(err) {
		t.Fatal("default export escaped the portable application data directory")
	}
}
