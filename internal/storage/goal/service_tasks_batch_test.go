package goal

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestService_AddTasksRefreshesProgressAndKeepsProjectScope(t *testing.T) {
	_, store := newTestStorage(t)
	ctx := context.Background()
	a, b := NewProjectService(store, "project-a"), NewProjectService(store, "project-b")
	ga, err := a.Set(ctx, "Objective A", "", "fixture checked", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Verify(ctx, "", true, "fixture checked"); err != nil {
		t.Fatal(err)
	}
	gb, err := b.Set(ctx, "Objective B", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	global, err := b.SetGlobal(ctx, "Global objective", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	added, err := a.AddTasks(ctx, "", []string{"Inspect", "Implement", "Verify"})
	if err != nil || len(added) != 3 {
		t.Fatalf("batch=%+v err=%v", added, err)
	}
	if g := a.Active(); g == nil || g.ID != ga.ID || g.ProjectKey != "project-a" || g.VerificationStatus != VerificationNone {
		t.Fatalf("active goal stale or wrong: %+v", g)
	}
	if p := a.Progress(); p.Total != 3 || p.Done != 0 || p.Verification != "" || p.Title != ga.Title {
		t.Fatalf("stale progress: %+v", p)
	}
	beforeB := b.Progress()
	if rejected, err := b.AddTasks(ctx, ga.ID, []string{"Foreign first", "Foreign second"}); !errors.Is(err, ErrNotFound) || rejected != nil {
		t.Fatalf("foreign goal accepted: %+v %v", rejected, err)
	}
	if !reflect.DeepEqual(b.Progress(), beforeB) {
		t.Fatal("failed foreign mutation changed progress")
	}
	if tasks, err := store.ListTasks(ctx, gb.ID); err != nil || len(tasks) != 0 {
		t.Fatalf("project B changed: %+v %v", tasks, err)
	}
	if tasks, err := store.ListTasks(ctx, ga.ID); err != nil || len(tasks) != 3 {
		t.Fatalf("foreign mutation reached A: %+v %v", tasks, err)
	}
	c := NewProjectService(store, "project-c")
	if active, err := c.Refresh(ctx); err != nil || active == nil || active.ID != global.ID {
		t.Fatalf("global fallback: %+v %v", active, err)
	}
	if added, err := c.AddTasks(ctx, "", []string{"Global first", "Global second"}); err != nil || len(added) != 2 {
		t.Fatalf("global batch: %+v %v", added, err)
	}
	if p := c.Progress(); p.Total != 2 || p.Title != global.Title {
		t.Fatalf("global progress: %+v", p)
	}
	if a.Progress().Total != 3 || b.Progress().Total != 0 {
		t.Fatal("global mutation replaced a project snapshot")
	}
}

func TestService_AddTasksFailuresPreserveCachedAndStoredState(t *testing.T) {
	db, store := newTestStorage(t)
	ctx := context.Background()
	svc := NewProjectService(store, "project")
	if added, err := svc.AddTasks(ctx, "", []string{"first"}); err == nil || added != nil {
		t.Fatalf("no goal accepted: %+v %v", added, err)
	}
	g, err := svc.Set(ctx, "Synthetic objective", "", "fixture checked", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Verify(ctx, "", true, "fixture checked"); err != nil {
		t.Fatal(err)
	}
	before := svc.Progress()
	verified, err := svc.Goal(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_service_batch BEFORE INSERT ON goal_tasks WHEN NEW.title='second' BEGIN SELECT RAISE(ABORT,'fixture second insert failure'); END`); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	for _, tc := range []struct {
		name   string
		ctx    context.Context
		titles []string
	}{
		{"invalid second title", ctx, []string{"first", " "}},
		{"SQL second insert", ctx, []string{"first", "second", "third"}},
		{"canceled", canceled, []string{"first", "second"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if added, err := svc.AddTasks(tc.ctx, "", tc.titles); err == nil || added != nil {
				t.Fatalf("invalid batch accepted: %+v %v", added, err)
			}
			if !reflect.DeepEqual(svc.Progress(), before) || svc.Active().VerificationStatus != VerificationPassed {
				t.Fatalf("failed batch refreshed partial state: %+v", svc.Progress())
			}
			assertBatchUnchanged(t, store, verified)
		})
	}
}
