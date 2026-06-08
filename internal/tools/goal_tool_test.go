package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"supercli/internal/goal"
	"supercli/internal/storage"
)

// newGoalTestService opens a fresh in-memory-ish SQLite
// db, runs the goal migrations, and returns a Service.
func newGoalTestService(t *testing.T) *goal.Service {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(dir)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	gs := goal.NewStorage(db)
	if err := gs.Migrate(context.Background()); err != nil {
		t.Fatalf("goal.Migrate: %v", err)
	}
	svc := goal.NewService(gs)
	if _, err := svc.Refresh(context.Background()); err != nil {
		t.Fatalf("svc.Refresh: %v", err)
	}
	return svc
}

func TestGoalTool_Show_NoActive(t *testing.T) {
	tool := NewGoalTool(newGoalTestService(t))
	res, err := tool.Execute(context.Background(), jsonRaw(t, `{"action":"show"}`))
	if err != nil || res.Err != nil {
		t.Fatalf("err=%v res.Err=%v", err, res.Err)
	}
	if !strings.Contains(res.Text, "no active goal") {
		t.Errorf("got %q", res.Text)
	}
}

func TestGoalTool_Show_Active(t *testing.T) {
	svc := newGoalTestService(t)
	ctx := context.Background()
	_, _ = svc.Set(ctx, "ship F8", "the goal", "all green", "")
	tool := NewGoalTool(svc)
	res, err := tool.Execute(ctx, jsonRaw(t, `{"action":"show"}`))
	if err != nil || res.Err != nil {
		t.Fatalf("err=%v res.Err=%v", err, res.Err)
	}
	if !strings.Contains(res.Text, "ship F8") {
		t.Errorf("got %q", res.Text)
	}
}

func TestGoalTool_List_Empty(t *testing.T) {
	tool := NewGoalTool(newGoalTestService(t))
	res, _ := tool.Execute(context.Background(), jsonRaw(t, `{"action":"list"}`))
	if !strings.Contains(res.Text, "no goals yet") {
		t.Errorf("got %q", res.Text)
	}
}

func TestGoalTool_List(t *testing.T) {
	svc := newGoalTestService(t)
	ctx := context.Background()
	_, _ = svc.Set(ctx, "a", "", "", "")
	res, _ := NewGoalTool(svc).Execute(ctx, jsonRaw(t, `{"action":"list"}`))
	if !strings.Contains(res.Text, "1 goal") || !strings.Contains(res.Text, "a") {
		t.Errorf("got %q", res.Text)
	}
}

func TestGoalTool_Tasks_Empty(t *testing.T) {
	svc := newGoalTestService(t)
	ctx := context.Background()
	_, _ = svc.Set(ctx, "x", "", "", "")
	res, _ := NewGoalTool(svc).Execute(ctx, jsonRaw(t, `{"action":"tasks"}`))
	if !strings.Contains(res.Text, "no tasks") {
		t.Errorf("got %q", res.Text)
	}
}

func TestGoalTool_Tasks_WithTasks(t *testing.T) {
	svc := newGoalTestService(t)
	ctx := context.Background()
	_, _ = svc.Set(ctx, "x", "", "", "")
	_, _ = svc.AddTask(ctx, "", "first")
	_, _ = svc.AddTask(ctx, "", "second")
	res, _ := NewGoalTool(svc).Execute(ctx, jsonRaw(t, `{"action":"tasks"}`))
	if !strings.Contains(res.Text, "2 task") || !strings.Contains(res.Text, "first") {
		t.Errorf("got %q", res.Text)
	}
}

func TestGoalTool_AddTask(t *testing.T) {
	svc := newGoalTestService(t)
	ctx := context.Background()
	_, _ = svc.Set(ctx, "x", "", "", "")
	res, err := NewGoalTool(svc).Execute(ctx, jsonRaw(t, `{"action":"add_task","title":"new"}`))
	if err != nil || res.Err != nil {
		t.Fatalf("err=%v res.Err=%v", err, res.Err)
	}
	if !strings.Contains(res.Text, "added task 1: new") {
		t.Errorf("got %q", res.Text)
	}
}

func TestGoalTool_CompleteTask(t *testing.T) {
	svc := newGoalTestService(t)
	ctx := context.Background()
	_, _ = svc.Set(ctx, "x", "", "", "")
	_, _ = svc.AddTask(ctx, "", "do")
	res, _ := NewGoalTool(svc).Execute(ctx, jsonRaw(t, `{"action":"complete_task","task_seq":1}`))
	if !strings.Contains(res.Text, "task 1 -> done") {
		t.Errorf("got %q", res.Text)
	}
}

func TestGoalTool_CompleteTask_NoSeq(t *testing.T) {
	svc := newGoalTestService(t)
	ctx := context.Background()
	_, _ = svc.Set(ctx, "x", "", "", "")
	res, err := NewGoalTool(svc).Execute(ctx, jsonRaw(t, `{"action":"complete_task"}`))
	if err == nil {
		t.Errorf("expected error, got %+v", res)
	}
}

func TestGoalTool_SkipTask(t *testing.T) {
	svc := newGoalTestService(t)
	ctx := context.Background()
	_, _ = svc.Set(ctx, "x", "", "", "")
	_, _ = svc.AddTask(ctx, "", "do")
	res, _ := NewGoalTool(svc).Execute(ctx, jsonRaw(t, `{"action":"skip_task","task_seq":1}`))
	if !strings.Contains(res.Text, "skipped") {
		t.Errorf("got %q", res.Text)
	}
}

func TestGoalTool_AddNote(t *testing.T) {
	svc := newGoalTestService(t)
	ctx := context.Background()
	_, _ = svc.Set(ctx, "x", "", "", "")
	res, _ := NewGoalTool(svc).Execute(ctx, jsonRaw(t, `{"action":"add_note","text":"hi"}`))
	if !strings.Contains(res.Text, "note appended") {
		t.Errorf("got %q", res.Text)
	}
}

func TestGoalTool_MarkDone(t *testing.T) {
	svc := newGoalTestService(t)
	ctx := context.Background()
	_, _ = svc.Set(ctx, "x", "", "", "")
	res, _ := NewGoalTool(svc).Execute(ctx, jsonRaw(t, `{"action":"mark_done"}`))
	if !strings.Contains(res.Text, "done") {
		t.Errorf("got %q", res.Text)
	}
	if svc.Active() != nil {
		t.Error("active should be cleared after mark_done")
	}
}

func TestGoalTool_Abandon(t *testing.T) {
	svc := newGoalTestService(t)
	ctx := context.Background()
	_, _ = svc.Set(ctx, "x", "", "", "")
	res, _ := NewGoalTool(svc).Execute(ctx, jsonRaw(t, `{"action":"abandon"}`))
	if !strings.Contains(res.Text, "abandoned") {
		t.Errorf("got %q", res.Text)
	}
}

func TestGoalTool_Decompose_Heuristic(t *testing.T) {
	svc := newGoalTestService(t)
	ctx := context.Background()
	_, _ = svc.Set(ctx, "x", "", "", "")
	tool := NewGoalTool(svc)
	tool.SetNow(func() time.Time { return time.Now() })
	// No DecomposeProvider set -> heuristic.
	res, _ := tool.Execute(ctx, jsonRaw(t, `{"action":"decompose","title":"ship F8"}`))
	if !strings.Contains(res.Text, "added") {
		t.Errorf("got %q", res.Text)
	}
	// The active goal should have new tasks.
	tasks, _ := svc.ListTasks(ctx, "")
	if len(tasks) < 2 {
		t.Errorf("expected tasks added, got %d", len(tasks))
	}
}

func TestGoalTool_Decompose_WithProvider(t *testing.T) {
	svc := newGoalTestService(t)
	ctx := context.Background()
	_, _ = svc.Set(ctx, "x", "", "", "")
	tool := NewGoalTool(svc)
	tool.SetDecompose(stubGoalProvider{resp: `{"action":"decompose","tasks":["alpha","beta","gamma"]}`}, "m")
	res, _ := tool.Execute(ctx, jsonRaw(t, `{"action":"decompose","title":"x"}`))
	if !strings.Contains(res.Text, "3 tasks") || !strings.Contains(res.Text, "alpha") {
		t.Errorf("got %q", res.Text)
	}
}

func TestGoalTool_BadAction(t *testing.T) {
	tool := NewGoalTool(newGoalTestService(t))
	_, err := tool.Execute(context.Background(), jsonRaw(t, `{"action":"scribble"}`))
	if err == nil {
		t.Error("expected error for unknown action")
	}
}

func TestGoalTool_BadJSON(t *testing.T) {
	tool := NewGoalTool(newGoalTestService(t))
	// trailing comma is invalid JSON
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"action": "show",}`))
	if err == nil {
		t.Error("expected error for bad json")
	}
}

func TestGoalTool_NilService(t *testing.T) {
	tool := &GoalTool{Service: nil}
	_, err := tool.Execute(context.Background(), jsonRaw(t, `{"action":"show"}`))
	if err == nil {
		t.Error("expected error when service is nil")
	}
}

func TestGoalTool_Show_NotFound(t *testing.T) {
	svc := newGoalTestService(t)
	res, err := NewGoalTool(svc).Execute(context.Background(), jsonRaw(t, `{"action":"show","goal_id":"g-nope"}`))
	if err == nil {
		t.Errorf("expected error, got %+v", res)
	}
}

func TestGoalTool_Decompose_NoGoal(t *testing.T) {
	svc := newGoalTestService(t)
	res, _ := NewGoalTool(svc).Execute(context.Background(), jsonRaw(t, `{"action":"decompose","title":"x"}`))
	if !strings.Contains(res.Text, "no goal to attach") {
		t.Errorf("got %q", res.Text)
	}
}

func TestGoalTool_AddTask_NoActive(t *testing.T) {
	svc := newGoalTestService(t)
	_, err := NewGoalTool(svc).Execute(context.Background(), jsonRaw(t, `{"action":"add_task","title":"x"}`))
	if err == nil {
		t.Error("expected error for no active goal")
	}
}

func TestGoalTool_AddNote_NoActive(t *testing.T) {
	svc := newGoalTestService(t)
	_, err := NewGoalTool(svc).Execute(context.Background(), jsonRaw(t, `{"action":"add_note","text":"x"}`))
	if err == nil {
		t.Error("expected error for no active goal")
	}
}

// stubGoalProvider is a minimal goal.Provider for tests.
type stubGoalProvider struct{ resp string }

func (s stubGoalProvider) Complete(_ context.Context, _ []goal.Message) (string, error) {
	return s.resp, nil
}

func jsonRaw(t *testing.T, s string) json.RawMessage {
	t.Helper()
	if !json.Valid([]byte(s)) {
		t.Fatalf("invalid json: %s", s)
	}
	return json.RawMessage(s)
}
