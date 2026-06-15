package goal

import (
	"context"
	"strings"
	"testing"
	"time"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	_, gs := newTestStorage(t)
	return NewService(gs)
}

func TestService_Refresh_NoActive(t *testing.T) {
	svc := newTestService(t)
	g, err := svc.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if g != nil {
		t.Errorf("expected nil active, got %+v", g)
	}
	if svc.Active() != nil {
		t.Error("Active() should be nil after Refresh with no rows")
	}
}

func TestService_Refresh_WithActive(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	if _, err := svc.Set(ctx, "ship F8", "long-term", "", ""); err != nil {
		t.Fatal(err)
	}
	// Refresh should pick it up.
	svc.active = nil // forcibly clear cache
	if _, err := svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if svc.Active() == nil {
		t.Error("Active() should be set after Refresh")
	}
	if svc.Active().Title != "ship F8" {
		t.Errorf("title = %q", svc.Active().Title)
	}
}

func TestService_Set_PausesPrior(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	a, _ := svc.Set(ctx, "first", "", "", "")
	b, _ := svc.Set(ctx, "second", "", "", "")
	if svc.Active().ID != b.ID {
		t.Errorf("active = %q, want %q", svc.Active().ID, b.ID)
	}
	// First should now be paused.
	old, _ := svc.Goal(ctx, a.ID)
	if old.Status != StatusPaused {
		t.Errorf("prior goal status = %q, want paused", old.Status)
	}
}

func TestService_Set_EmptyTitle(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.Set(context.Background(), "   ", "", "", "")
	if err != ErrEmptyTitle {
		t.Errorf("expected ErrEmptyTitle, got %v", err)
	}
}

func TestService_AddTask_DefaultsToActive(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	g, _ := svc.Set(ctx, "x", "", "", "")
	t1, err := svc.AddTask(ctx, "", "do thing")
	if err != nil {
		t.Fatal(err)
	}
	if t1.GoalID != g.ID {
		t.Errorf("task on %q, want %q", t1.GoalID, g.ID)
	}
}

func TestService_AddTask_NoActive(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.AddTask(context.Background(), "", "do thing")
	if err == nil {
		t.Error("expected error when no active goal")
	}
}

func TestService_SetTaskStatus_DefaultsToActive(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	_, _ = svc.Set(ctx, "x", "", "", "")
	_, _ = svc.AddTask(ctx, "", "do thing")
	if err := svc.SetTaskStatus(ctx, "", 1, TaskDone); err != nil {
		t.Fatal(err)
	}
}

func TestService_SetStatus_ClearsActiveOnTerminal(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	g, _ := svc.Set(ctx, "x", "", "", "")
	if err := svc.SetStatus(ctx, g.ID, StatusDone); err != nil {
		t.Fatal(err)
	}
	if svc.Active() != nil {
		t.Error("Active() should be nil after terminal status")
	}
	// ActiveGoal in storage should also be nil.
	cur, _ := svc.Refresh(ctx)
	if cur != nil {
		t.Error("storage should not return an active after SetStatus(done)")
	}
}

func TestService_Inject_NoActive_ReturnsBase(t *testing.T) {
	svc := newTestService(t)
	got, err := svc.Inject(context.Background(), "base prompt", 5)
	if err != nil {
		t.Fatal(err)
	}
	if got != "base prompt" {
		t.Errorf("Inject = %q, want base prompt unchanged", got)
	}
}

func TestService_Inject_WithActive_ContainsTitle(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	_, _ = svc.Set(ctx, "ship F8", "long-term goal", "all features green", "")
	_, _ = svc.AddTask(ctx, "", "design doc")
	_, _ = svc.AddTask(ctx, "", "implement")
	got, err := svc.Inject(ctx, "base", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"base", "[current_goal]", "ship F8",
		"long-term goal", "all features green", "open_tasks:",
		"1. design doc", "2. implement", "[end current_goal]"} {
		if !strings.Contains(got, want) {
			t.Errorf("Inject missing %q", want)
		}
	}
}

func TestService_Inject_SkipsDoneTasks(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	_, _ = svc.Set(ctx, "x", "", "", "")
	_, _ = svc.AddTask(ctx, "", "a")
	t2, _ := svc.AddTask(ctx, "", "b")
	_ = svc.SetTaskStatus(ctx, "", t2.Seq, TaskDone)
	got, _ := svc.Inject(ctx, "base", 5)
	if !strings.Contains(got, "1. a") {
		t.Errorf("pending task 'a' should appear: %q", got)
	}
	if strings.Contains(got, "2. b") {
		t.Errorf("done task 'b' should not appear: %q", got)
	}
}

func TestService_Inject_LimitsTasks(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	_, _ = svc.Set(ctx, "x", "", "", "")
	for i := 0; i < 10; i++ {
		_, _ = svc.AddTask(ctx, "", "task")
	}
	got, _ := svc.Inject(ctx, "base", 3)
	// Count occurrences of "- [pending]" lines.
	count := strings.Count(got, "- [pending]")
	if count > 3 {
		t.Errorf("expected <= 3 task lines, got %d", count)
	}
}

func TestService_StatusLine_Empty(t *testing.T) {
	svc := newTestService(t)
	if got := svc.StatusLine(context.Background()); got != "" {
		t.Errorf("StatusLine = %q, want empty", got)
	}
}

func TestService_StatusLine_NoTasks(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	_, _ = svc.Set(ctx, "ship F8", "", "", "")
	got := svc.StatusLine(ctx)
	if !strings.Contains(got, "goal: ship F8") {
		t.Errorf("StatusLine = %q, want goal: ship F8", got)
	}
	if strings.Contains(got, "(0/") {
		t.Errorf("StatusLine should not show 0/0: %q", got)
	}
}

func TestService_StatusLine_WithProgress(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	_, _ = svc.Set(ctx, "ship F8", "", "", "")
	for i := 0; i < 5; i++ {
		_, _ = svc.AddTask(ctx, "", "task")
	}
	t1, _ := svc.AddTask(ctx, "", "task 6")
	_ = svc.SetTaskStatus(ctx, "", t1.Seq, TaskDone)
	got := svc.StatusLine(ctx)
	if !strings.Contains(got, "1/6") {
		t.Errorf("StatusLine = %q, want 1/6", got)
	}
}

func TestService_NilSafe(t *testing.T) {
	var s *Service
	if s.Active() != nil {
		t.Error("nil Active() should return nil")
	}
	if got, _ := s.Inject(context.Background(), "base", 5); got != "base" {
		t.Error("nil Inject should return base")
	}
	if got := s.StatusLine(context.Background()); got != "" {
		t.Error("nil StatusLine should return empty")
	}
}

func TestService_AppendNote_DefaultsToActive(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	_, _ = svc.Set(ctx, "x", "", "", "")
	if err := svc.AppendNote(ctx, "", "first note"); err != nil {
		t.Fatal(err)
	}
	g, _ := svc.Goal(ctx, svc.Active().ID)
	if !strings.Contains(g.Notes, "first note") {
		t.Errorf("note not stored: %q", g.Notes)
	}
}

func TestService_RefreshAfterMutation(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	g, _ := svc.Set(ctx, "x", "", "", "")
	// Manually clear cache.
	svc.active = nil
	if _, err := svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if svc.Active() == nil || svc.Active().ID != g.ID {
		t.Error("Refresh should reload from SQLite")
	}
	// The Refresh timestamp should advance.
	t1 := svc.loadedAt
	time.Sleep(time.Millisecond)
	_, _ = svc.Refresh(ctx)
	if !svc.loadedAt.After(t1) {
		t.Errorf("loadedAt should advance, got %v after %v", svc.loadedAt, t1)
	}
}
