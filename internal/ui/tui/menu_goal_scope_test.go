package tui

import (
	"context"
	"supercli/internal/storage"
	"supercli/internal/storage/goal"
	"testing"
)

func TestGoalMenuAssignsLegacyAndCreatesGlobalGoal(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := goal.NewStorage(db)
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	old, err := goal.NewService(store).Set(ctx, "Windows work", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	svc := goal.NewProjectService(store, "usos")
	m := New(Options{Home: t.TempDir(), GoalService: svc, Language: "en"})
	next, _ := m.openGoalMenu()
	m = next.(Model)
	selectRow := func(id string) {
		t.Helper()
		for i, row := range m.goalMenuRows() {
			if row.id == id {
				m.menu.cursor = i
				next, _ := m.selectGoalAction()
				m = next.(Model)
				return
			}
		}
		t.Fatalf("missing action %s", id)
	}
	selectRow("assign_project")
	if active := svc.Active(); active == nil || active.ID != old.ID || len(m.goalUnassigned) != 0 {
		t.Fatalf("assignment failed: %+v", active)
	}
	selectRow("scope_global")
	selectRow("new")
	m.menu.form = []string{"General goal", "", ""}
	m.menu.formAt = 2
	next, _ = m.submitGoalForm()
	m = next.(Model)
	if m.menu.filter != "global" || m.globalGoalSvc.Active() == nil || m.globalGoalSvc.Active().Title != "General goal" {
		t.Fatal("global form lost selected scope")
	}
	selectRow("task")
	m.menu.form = []string{"General task"}
	next, _ = m.submitGoalForm()
	m = next.(Model)
	tasks, err := m.globalGoalSvc.ListTasks(ctx, "")
	if err != nil || len(tasks) != 1 || tasks[0].Title != "General task" {
		t.Fatalf("wrong global tasks: %+v %v", tasks, err)
	}
	if active, _ := svc.Refresh(ctx); active.ID != old.ID {
		t.Fatal("global creation replaced project goal")
	}
	if tasks, _ := svc.ListTasks(ctx, ""); len(tasks) != 0 {
		t.Fatal("global task leaked into project")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range m.goalMenuRows() {
		if row.id == "toggle" {
			found = true
		}
	}
	if !found {
		t.Fatal("render re-read SQLite instead of collected task rows")
	}
}

func TestGlobalGoalEditsRefreshProjectFooterImmediately(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := goal.NewStorage(db)
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	svc := goal.NewProjectService(store, "project")
	m := New(Options{Home: t.TempDir(), GoalService: svc, Language: "en"})
	next, _ := m.openGoalMenu()
	m = next.(Model)
	selectRow := func(id string) {
		t.Helper()
		for i, row := range m.goalMenuRows() {
			if row.id == id {
				m.menu.cursor = i
				next, _ := m.selectGoalAction()
				m = next.(Model)
				return
			}
		}
		t.Fatalf("missing action %s", id)
	}
	selectRow("scope_global")
	selectRow("new")
	m.menu.form = []string{"Global fallback", "", ""}
	m.menu.formAt = 2
	next, _ = m.submitGoalForm()
	m = next.(Model)
	if got := svc.Progress(); got.Title != "Global fallback" {
		t.Fatalf("footer still shows old goal: %+v", got)
	}
	selectRow("task")
	m.menu.form = []string{"Global task"}
	next, _ = m.submitGoalForm()
	m = next.(Model)
	if got := svc.Progress(); got.Total != 1 || got.Done != 0 {
		t.Fatalf("footer missed new task: %+v", got)
	}
	selectRow("toggle")
	if got := svc.Progress(); got.Total != 1 || got.Done != 1 {
		t.Fatalf("footer missed completed task: %+v", got)
	}
	selectRow("pause")
	if got := svc.Progress(); got.Title != "" {
		t.Fatalf("footer still shows paused goal: %+v", got)
	}
}
