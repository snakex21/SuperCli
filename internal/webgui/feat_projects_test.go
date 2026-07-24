package webgui

import (
	"os"
	"path/filepath"
	"testing"

	"supercli/internal/storage/memory"
	"supercli/internal/system/config"
)

func TestProjectActionAddCreatesStorageAndSwitchesHome(t *testing.T) {
	dataDir := t.TempDir()
	home := t.TempDir()
	proj := t.TempDir()
	eng, err := NewEngine(config.Config{Provider: config.ProviderEcho, Model: "echo"}, home, dataDir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	if err := eng.projectAction("add", proj, ""); err != nil {
		t.Fatalf("projectAction(add): %v", err)
	}
	if got := eng.Home(); got != proj {
		t.Fatalf("Home after add = %q, want %q", got, proj)
	}
	ws := memory.LoadWorkspace(dataDir)
	if ws.Active != proj {
		t.Fatalf("workspace active = %q, want %q", ws.Active, proj)
	}
	if _, ok := ws.Get(proj); !ok {
		t.Fatalf("workspace missing project %q", proj)
	}
	m := memory.LoadProjectsMap(dataDir)
	if m[proj] != memory.ProjectKey(proj) {
		t.Fatalf("projects map[%q] = %q, want %q", proj, m[proj], memory.ProjectKey(proj))
	}
	memDB := filepath.Join(dataDir, "projects", memory.ProjectKey(proj), "memory.db")
	if _, err := os.Stat(memDB); err != nil {
		t.Fatalf("project memory db not initialized at %s: %v", memDB, err)
	}
}

func TestProjectActionUseSwitchesHomeImmediately(t *testing.T) {
	dataDir := t.TempDir()
	home := t.TempDir()
	projA := t.TempDir()
	projB := t.TempDir()
	ws := &memory.Workspace{}
	ws.Upsert(memory.Project{Name: "a", Path: projA})
	ws.Upsert(memory.Project{Name: "b", Path: projB})
	if err := memory.SaveWorkspace(dataDir, ws); err != nil {
		t.Fatalf("SaveWorkspace: %v", err)
	}
	eng, err := NewEngine(config.Config{Provider: config.ProviderEcho, Model: "echo"}, home, dataDir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	if err := eng.projectAction("use", "b", ""); err != nil {
		t.Fatalf("projectAction(use): %v", err)
	}
	if got := eng.Home(); got != projB {
		t.Fatalf("Home after use = %q, want %q", got, projB)
	}
	got := memory.LoadWorkspace(dataDir)
	if got.Active != projB {
		t.Fatalf("workspace active = %q, want %q", got.Active, projB)
	}
}

func TestProjectActionRemoveDropsLegacyMapEntry(t *testing.T) {
	dataDir := t.TempDir()
	home := t.TempDir()
	proj := t.TempDir()
	eng, err := NewEngine(config.Config{Provider: config.ProviderEcho, Model: "echo"}, home, dataDir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err := eng.projectAction("add", proj, "Temp"); err != nil {
		t.Fatalf("projectAction(add): %v", err)
	}
	if err := eng.projectAction("remove", proj, ""); err != nil {
		t.Fatalf("projectAction(remove): %v", err)
	}
	if m := memory.LoadProjectsMap(dataDir); m[proj] != "" {
		t.Fatalf("legacy map still has %q after remove", proj)
	}
	// The merged listing must not resurrect the removed project.
	for _, p := range eng.listProjects() {
		if p.Path == proj {
			t.Fatalf("removed project %q reappeared in listing", proj)
		}
	}
}
