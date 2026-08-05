package webgui

import (
	"context"
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

	if err := eng.projectAction("add", proj, "", ""); err != nil {
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

func TestProjectActionRelocatePreservesProjectState(t *testing.T) {
	dataDir := t.TempDir()
	home := t.TempDir()
	oldPath := t.TempDir()
	newPath := t.TempDir()
	eng, err := NewEngine(config.Config{Provider: config.ProviderEcho, Model: "echo"}, home, dataDir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()
	if err := eng.projectAction("add", oldPath, "USB project", ""); err != nil {
		t.Fatal(err)
	}
	oldKey := memory.ProjectStorageKey(dataDir, oldPath)
	projectStore, err := memory.OpenProjectStore(dataDir, oldPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := projectStore.Put(memory.Entry{ID: "relocate-fact", Scope: memory.ScopeFact, Content: "keep me"}); err != nil {
		t.Fatal(err)
	}
	projectStore.Close()
	sessions, err := eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := sessions.Create(oldPath, "echo", "USB chat")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.EnqueueTask(context.Background(), oldPath, sess.ID, "queued"); err != nil {
		t.Fatal(err)
	}
	schedule, err := eng.schedules.Create("0 9 * * *", "scheduled", oldPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := eng.projectAction("relocate", oldPath, "", newPath); err != nil {
		t.Fatalf("projectAction(relocate): %v", err)
	}
	if eng.Home() != newPath {
		t.Fatalf("home = %q, want %q", eng.Home(), newPath)
	}
	projects := memory.LoadProjectsMap(dataDir)
	if projects[oldPath] != "" || projects[newPath] != oldKey {
		t.Fatalf("relocated projects map = %+v", projects)
	}
	ws := memory.LoadWorkspace(dataDir)
	moved, ok := ws.Get(newPath)
	if !ok || moved.Name != "USB project" || ws.Active != newPath {
		t.Fatalf("relocated workspace = %+v", ws)
	}
	projectStore, err = memory.OpenProjectStore(dataDir, newPath)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := projectStore.Recent(memory.ScopeFact, 5)
	projectStore.Close()
	if err != nil || len(facts) != 1 || facts[0].Content != "keep me" {
		t.Fatalf("relocated memory = %+v, err=%v", facts, err)
	}
	gotSession, err := sessions.Get(sess.ID)
	if err != nil || gotSession.Cwd != newPath {
		t.Fatalf("relocated session = %+v, err=%v", gotSession, err)
	}
	queue, err := sessions.ListQueuedTasks(context.Background(), newPath)
	if err != nil || len(queue) != 1 || queue[0].SessionID != sess.ID {
		t.Fatalf("relocated queue = %+v, err=%v", queue, err)
	}
	schedules := eng.schedules.List(newPath)
	if len(schedules) != 1 || schedules[0].ID != schedule.ID {
		t.Fatalf("relocated schedules = %+v", schedules)
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

	if err := eng.projectAction("use", "b", "", ""); err != nil {
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
	if err := eng.projectAction("add", proj, "Temp", ""); err != nil {
		t.Fatalf("projectAction(add): %v", err)
	}
	if err := eng.projectAction("remove", proj, "", ""); err != nil {
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
