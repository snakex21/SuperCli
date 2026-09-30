package goal

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestProjectGoalsStayIndependentAndGlobalIsExplicit(t *testing.T) {
	_, store := newTestStorage(t)
	ctx := context.Background()
	a, b := NewProjectService(store, "usos-a"), NewProjectService(store, "game-b")
	ga, err := a.Set(ctx, "Fix Windows setup", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if g, err := b.Refresh(ctx); err != nil || g != nil {
		t.Fatalf("project B leaked project A: %+v %v", g, err)
	}
	gb, err := b.Set(ctx, "Ship game", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if g, _ := store.GetGoal(ctx, ga.ID); g.Status != StatusActive {
		t.Fatalf("B paused A: %+v", g)
	}
	global, err := b.SetGlobal(ctx, "General objective", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, svc := range []*Service{a, b} {
		g, err := svc.Refresh(ctx)
		if err != nil || g.ProjectKey == GlobalProjectKey {
			t.Fatalf("global replaced project goal: %+v %v", g, err)
		}
	}
	c := NewProjectService(store, "third-project")
	if g, err := c.Refresh(ctx); err != nil || g.ID != global.ID {
		t.Fatalf("explicit global fallback: %+v %v", g, err)
	}
	injected, err := a.Inject(ctx, "base", 5)
	if err != nil || !strings.Contains(injected, ga.Title) || strings.Contains(injected, gb.Title) || strings.Contains(injected, global.Title) {
		t.Fatalf("wrong model context: %s %v", injected, err)
	}
	for _, fn := range []func() error{
		func() error { _, err := b.Goal(ctx, ga.ID); return err },
		func() error { _, err := b.AddTask(ctx, ga.ID, "wrong"); return err },
		func() error { return b.AppendNote(ctx, ga.ID, "wrong") },
		func() error { return b.SetTaskStatus(ctx, ga.ID, 1, TaskDone) },
		func() error { return b.SetStatus(ctx, ga.ID, StatusAbandoned) },
		func() error { return b.Verify(ctx, ga.ID, true, "wrong") },
		func() error { _, err := b.ListTasks(ctx, ga.ID); return err },
		func() error { return b.Assign(ctx, ga.ID, false) },
	} {
		if err := fn(); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign goal accepted: %v", err)
		}
	}
	goals, err := b.List(ctx)
	if err != nil || len(goals) != 2 {
		t.Fatalf("visible goals: %+v %v", goals, err)
	}
}

func TestLegacyGoalsRequireAssignmentAndKeepAllTheirData(t *testing.T) {
	_, store := newTestStorage(t)
	ctx := context.Background()
	legacy := NewService(store)
	g, err := legacy.Set(ctx, "Legacy Windows work", "description", "criteria", "old-session")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.AddTask(ctx, g.ID, "Existing task"); err != nil {
		t.Fatal(err)
	}
	if err := legacy.AppendNote(ctx, g.ID, "Existing note"); err != nil {
		t.Fatal(err)
	}
	scoped := NewProjectService(store, "usos")
	if active, err := scoped.Refresh(ctx); err != nil || active != nil {
		t.Fatalf("unassigned leaked: %+v %v", active, err)
	}
	pending, err := scoped.Unassigned(ctx)
	if err != nil || len(pending) != 1 || pending[0].ID != g.ID {
		t.Fatalf("legacy inaccessible: %+v %v", pending, err)
	}
	if err := scoped.Assign(ctx, g.ID, false); err != nil {
		t.Fatal(err)
	}
	reopened := NewProjectService(store, "usos")
	active, err := reopened.Refresh(ctx)
	if err != nil || active.ID != g.ID || active.ParentSessionID != "old-session" || !strings.Contains(active.Notes, "Existing note") || active.Description != "description" || active.SuccessCriteria != "criteria" {
		t.Fatalf("lost metadata: %+v %v", active, err)
	}
	tasks, err := reopened.ListTasks(ctx, "")
	if err != nil || len(tasks) != 1 || tasks[0].Title != "Existing task" {
		t.Fatalf("lost tasks: %+v %v", tasks, err)
	}
	if pending, _ := reopened.Unassigned(ctx); len(pending) != 0 {
		t.Fatal("assignment not persisted")
	}
	other := NewProjectService(store, "other")
	if active, _ := other.Refresh(ctx); active != nil {
		t.Fatal("assignment leaked into other project")
	}
	if err := reopened.Assign(ctx, g.ID, true); err != nil {
		t.Fatal(err)
	}
	if active, err := other.Refresh(ctx); err != nil || active.ID != g.ID || active.ProjectKey != GlobalProjectKey {
		t.Fatalf("global assignment: %+v %v", active, err)
	}
}

func TestFailedGoalReplacementRollsBackPause(t *testing.T) {
	_, store := newTestStorage(t)
	ctx := context.Background()
	svc := NewProjectService(store, "project")
	g, err := svc.Set(ctx, "Existing goal", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	duplicate := *g
	if err := store.replaceActive(ctx, &duplicate); err == nil {
		t.Fatal("duplicate id must fail")
	}
	if active, err := svc.Refresh(ctx); err != nil || active == nil || active.ID != g.ID {
		t.Fatalf("failed create paused current goal: %+v %v", active, err)
	}
}
