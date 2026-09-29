package tui

import (
	"context"
	"strings"
	"testing"
)

func TestUpdateActionOpensAllThreeOperations(t *testing.T) {
	m := New(Options{Language: "pl", NoColor: true})
	m.mode, m.menu.kind = modeMenu, menuActions
	for i, row := range m.actionRows() {
		if row.id == "update" {
			m.menu.cursor = i
			break
		}
	}
	next, _ := m.selectAction()
	m = next.(Model)
	if m.menu.kind != menuUpdate {
		t.Fatal("update action did not open its menu")
	}
	view := m.renderUpdateMenu()
	for _, key := range []string{"update.check", "update.download", "update.install"} {
		if !strings.Contains(view, m.tr(key)) {
			t.Fatalf("missing update action %s", key)
		}
	}
}

func TestUpdateInstallCannotInterruptActiveTask(t *testing.T) {
	called := false
	m := New(Options{Language: "pl", Commands: map[string]SlashHandler{
		"update": func(context.Context, string) (string, error) { called = true; return "", nil },
	}})
	m.busy = true
	next, _ := m.dispatchSlashCommand(SlashCommand{Name: "update", Args: "install"})
	m = next.(Model)
	if !m.busy || called || m.statusOverride != m.tr("update.busy") {
		t.Fatal("install disturbed the active task")
	}
	for _, action := range []string{"check", "download"} {
		next, cmd := m.dispatchSlashCommand(SlashCommand{Name: "update", Args: action})
		mm := next.(Model)
		if !mm.busy || cmd == nil {
			t.Fatalf("%s replaced task state", action)
		}
		msg := cmd().(slashResultMsg)
		if !msg.Local {
			t.Fatalf("%s would clear the active task on completion", action)
		}
	}
}

func TestUpdateInstallCannotInterruptWorkerWhileMainAgentIsIdle(t *testing.T) {
	called := false
	m := New(Options{Language: "en", Commands: map[string]SlashHandler{
		"update": func(context.Context, string) (string, error) { called = true; return "", nil },
	}})
	m.workerViews = []workerView{{id: "worker-1", status: "done"}, {id: "worker-2", status: "running"}}
	next, _ := m.dispatchSlashCommand(SlashCommand{Name: "update", Args: "install"})
	m = next.(Model)
	if called || m.busy || m.statusOverride != m.tr("update.busy") {
		t.Fatal("install disturbed a worker while the main agent was idle")
	}
}
