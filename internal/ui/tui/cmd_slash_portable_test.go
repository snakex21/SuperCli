package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"

	tea "github.com/charmbracelet/bubbletea"
)

// slashBuiltinRun dispatches a TUI builtin slash command by name.
func (m Model) slashBuiltinRun(t *testing.T, name, args string) (tea.Model, tea.Cmd) {
	t.Helper()
	h, ok := m.slashBuiltin(name)
	if !ok {
		t.Fatalf("/%s is not a builtin", name)
	}
	return h(m, SlashCommand{Name: name, Args: args})
}

func TestSlashExportSessionAndImportCreatesNewSession(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	store, err := session.OpenStore(data)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	src, err := store.Create(home, "qwen", "portable source")
	if err != nil {
		t.Fatal(err)
	}
	enc, _ := session.FromMessage(llm.Message{Role: llm.RoleUser, Content: "hello portable"})
	if err := store.AppendMessage(context.Background(), src.ID, enc); err != nil {
		t.Fatal(err)
	}
	m := New(Options{Home: home, DataDir: data, SessionID: src.ID, SessionStore: store})

	_, cmd := m.slashBuiltinRun(t, "export", "session")
	msg := cmd().(slashResultMsg)
	if msg.Err != nil {
		t.Fatal(msg.Err)
	}
	path := filepath.Join(data, session.PortableFilename(src))
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("default portable export missing: %v", err)
	}
	doc, err := session.DecodePortable(f)
	f.Close()
	if err != nil || doc.Session.Title != "portable source" {
		t.Fatalf("export not decodable: %v %+v", err, doc.Session)
	}
	if _, err := os.Stat(filepath.Join(data, "session")); !os.IsNotExist(err) {
		t.Fatal("/export session was treated as a markdown filename")
	}

	// Explicit relative path (json alias) resolves against the export dir.
	_, cmd = m.slashBuiltinRun(t, "export", "json copy.supercli-session.json")
	if msg := cmd().(slashResultMsg); msg.Err != nil {
		t.Fatal(msg.Err)
	}
	if _, err := os.Stat(filepath.Join(data, "copy.supercli-session.json")); err != nil {
		t.Fatal(err)
	}

	// Import from a quoted path relative to the TUI working directory.
	body, _ := os.ReadFile(path)
	if err := os.WriteFile(filepath.Join(home, "in file.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	mi := New(Options{Home: home, DataDir: data, SessionStore: store})
	_, cmd = mi.slashBuiltinRun(t, "import", `"in file.json"`)
	msg = cmd().(slashResultMsg)
	if msg.Err != nil {
		t.Fatal(msg.Err)
	}
	list, err := store.List(100)
	if err != nil {
		t.Fatal(err)
	}
	var imported *session.Session
	for i := range list {
		if list[i].ID != src.ID && list[i].Title == "portable source" {
			imported = &list[i]
		}
	}
	if imported == nil {
		t.Fatal("import did not create a new session")
	}
	if imported.Cwd != home {
		t.Fatalf("imported cwd = %q, want %q", imported.Cwd, home)
	}
	if !strings.Contains(msg.Body, imported.ID) || !strings.Contains(msg.Body, "/resume "+imported.ID) {
		t.Fatalf("import report lacks id/resume hint: %q", msg.Body)
	}
}

func TestSlashImportRejectsInvalidFile(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	store, err := session.OpenStore(data)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	bad := filepath.Join(home, "bad.supercli-session.json")
	if err := os.WriteFile(bad, []byte(`{"format":"nope"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m := New(Options{Home: home, DataDir: data, SessionStore: store})
	_, cmd := m.slashBuiltinRun(t, "import", bad)
	msg := cmd().(slashResultMsg)
	if msg.Err == nil || !strings.Contains(msg.Err.Error(), "not a valid SuperCli session file") {
		t.Fatalf("invalid file not rejected clearly: %v", msg.Err)
	}
	_, cmd = m.slashBuiltinRun(t, "import", filepath.Join(home, "missing.json"))
	if msg := cmd().(slashResultMsg); msg.Err == nil {
		t.Fatal("missing file accepted")
	}
}
