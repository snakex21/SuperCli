package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/llm"
	"supercli/internal/llm/providers"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

const tuiRenameSecret = "sk-tui-rename-test"

func newRenameTUIModel(t *testing.T) (Model, *providers.Manager, *session.Store, string) {
	t.Helper()
	dir := t.TempDir()
	mgr := providers.NewManager(dir)
	if err := mgr.Add("work", "openai", "http://127.0.0.1:1/v1", tuiRenameSecret, "m1"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Add("home", "openai", "http://127.0.0.1:2/v1", "", ""); err != nil {
		t.Fatal(err)
	}
	store, err := session.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	sess, err := store.Create(dir, "m1", "t")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetRuntime(sess.ID, "work", "m1", ""); err != nil {
		t.Fatal(err)
	}
	m := New(Options{Home: dir, DataDir: dir, ProviderMgr: mgr, SessionStore: store, ActiveProvider: "work",
		ModelContextStore: config.LoadModelContextStore(dir), CapabilityRegistry: llm.NewCapabilityRegistry()})
	return m, mgr, store, sess.ID
}

func TestSlashProvidersRenameUpdatesStateAndSessions(t *testing.T) {
	m, mgr, store, id := newRenameTUIModel(t)
	next, cmd := m.dispatchSlashCommand(SlashCommand{Name: "providers", Args: "rename work cloud"})
	m = next.(Model)
	msg, _ := cmd().(slashResultMsg)
	if msg.Err != nil {
		t.Fatal(msg.Err)
	}
	if m.activeProvider != "cloud" {
		t.Fatalf("activeProvider = %q", m.activeProvider)
	}
	if key, ok := mgr.APIKey("cloud"); !ok || key != tuiRenameSecret {
		t.Fatal("API key was not moved to the new name")
	}
	if got, _ := store.Get(id); got.Provider != "cloud" {
		t.Fatalf("session provider = %q", got.Provider)
	}
	_, cmd = m.dispatchSlashCommand(SlashCommand{Name: "providers", Args: "rename cloud HOME"})
	if msg, _ := cmd().(slashResultMsg); msg.Err == nil || !strings.Contains(msg.Err.Error(), "already exists") {
		t.Fatalf("collision not rejected: %v", msg.Err)
	}
	_, cmd = m.dispatchSlashCommand(SlashCommand{Name: "providers", Args: "rename cloud"})
	if msg, _ := cmd().(slashResultMsg); msg.Err != nil || !strings.Contains(msg.Body, "/providers rename") {
		t.Fatalf("usage not shown: %+v", msg)
	}
}

func TestProviderEditFormRenamesProvider(t *testing.T) {
	m, mgr, store, id := newRenameTUIModel(t)
	m.enterMenu(interactiveMenu{kind: menuProviders})
	m.enterMenu(interactiveMenu{kind: menuProviderForm, editName: "work",
		form: []string{"bad/name", "openai", "http://127.0.0.1:1/v1", tuiRenameSecret, "m1"}, formAt: 4})
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.menu.formErr == "" || m.menu.formAt != 0 || len(mgr.Names()) != 2 || mgr.Names()[0] != "work" {
		t.Fatalf("invalid name not rejected in form: err=%q names=%v", m.menu.formErr, mgr.Names())
	}
	m.menu.form[0], m.menu.formAt = "Work Cloud", 4
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.menu.kind != menuProviders {
		t.Fatalf("form did not close: %q", m.menu.formErr)
	}
	if key, ok := mgr.APIKey("Work Cloud"); !ok || key != tuiRenameSecret {
		t.Fatal("API key was not moved to the new name")
	}
	if _, ok := mgr.APIKey("work"); ok {
		t.Fatal("old provider still configured")
	}
	if m.activeProvider != "Work Cloud" {
		t.Fatalf("activeProvider = %q", m.activeProvider)
	}
	if got, _ := store.Get(id); got.Provider != "Work Cloud" {
		t.Fatalf("session provider = %q", got.Provider)
	}
}
