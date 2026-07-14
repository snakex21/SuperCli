package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"supercli/internal/storage"
	"supercli/internal/storage/goal"
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

func TestGoalTool_SetCreatesActiveGoal(t *testing.T) {
	svc := newGoalTestService(t)
	res, err := NewGoalTool(svc).Execute(context.Background(), jsonRaw(t,
		`{"action":"set","title":"ship web goals","description":"from the GUI","success_criteria":"all green"}`))
	if err != nil || res.Err != nil {
		t.Fatalf("err=%v res.Err=%v", err, res.Err)
	}
	active := svc.Active()
	if active == nil || active.Title != "ship web goals" || active.Description != "from the GUI" || active.SuccessCriteria != "all green" {
		t.Fatalf("active goal = %+v", active)
	}
	if !strings.Contains(res.Text, "active goal: ship web goals") {
		t.Errorf("got %q", res.Text)
	}
}

func TestGoalTool_SetRequiresTitle(t *testing.T) {
	res, err := NewGoalTool(newGoalTestService(t)).Execute(context.Background(), jsonRaw(t, `{"action":"set"}`))
	if err == nil || res.Err == nil {
		t.Fatalf("expected validation error, got err=%v result=%+v", err, res)
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

func TestGoalTool_CurrentGoalAliases(t *testing.T) {
	for _, alias := range []string{"current", "current_goal", "active", "active_goal", "goal_id", "<id>", "<goal_id>"} {
		t.Run(alias, func(t *testing.T) {
			svc := newGoalTestService(t)
			ctx := context.Background()
			_, _ = svc.Set(ctx, "ship", "", "", "")
			_, _ = svc.AddTask(ctx, "", "test it")
			res, err := NewGoalTool(svc).Execute(ctx, jsonRaw(t,
				fmt.Sprintf(`{"action":"complete_task","goal_id":%q,"task_seq":1}`, alias)))
			if err != nil || res.Err != nil {
				t.Fatalf("alias %q: err=%v result=%v", alias, err, res.Err)
			}
			tasks, _ := svc.ListTasks(ctx, "")
			if len(tasks) != 1 || tasks[0].Status != goal.TaskDone {
				t.Fatalf("alias %q did not update active task: %+v", alias, tasks)
			}
		})
	}
}

func TestGoalTool_ActiveTitleAsID(t *testing.T) {
	svc := newGoalTestService(t)
	ctx := context.Background()
	_, _ = svc.Set(ctx, "Napraw Add", "", "", "")
	_, _ = svc.AddTask(ctx, "", "test it")
	res, err := NewGoalTool(svc).Execute(ctx, jsonRaw(t,
		`{"action":"complete_task","goal_id":"napraw add","task_seq":1}`))
	if err != nil || res.Err != nil {
		t.Fatalf("active title alias: err=%v result=%v", err, res.Err)
	}
	tasks, _ := svc.ListTasks(ctx, "")
	if len(tasks) != 1 || tasks[0].Status != goal.TaskDone {
		t.Fatalf("active title did not resolve: %+v", tasks)
	}
}

func TestGoalTool_VerificationEvidenceAliases(t *testing.T) {
	for _, field := range []string{"result", "evidence"} {
		t.Run(field, func(t *testing.T) {
			svc := newGoalTestService(t)
			ctx := context.Background()
			created, _ := svc.Set(ctx, "ship", "", "tests pass", "")
			_, _ = svc.AddTask(ctx, "", "test it")
			_ = svc.SetTaskStatus(ctx, "", 1, goal.TaskDone)
			args := fmt.Sprintf(`{"action":"verify","passed":true,%q:"go test passed"}`, field)
			res, err := NewGoalTool(svc).Execute(ctx, jsonRaw(t, args))
			if err != nil || res.Err != nil {
				t.Fatalf("alias %q: err=%v result=%v", field, err, res.Err)
			}
			persisted, _ := svc.Goal(ctx, created.ID)
			if persisted.VerificationStatus != goal.VerificationPassed || persisted.VerificationEvidence != "go test passed" {
				t.Fatalf("alias %q did not persist evidence: %+v", field, persisted)
			}
		})
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
	verified, err := NewGoalTool(svc).Execute(ctx, jsonRaw(t, `{"action":"verify","passed":true,"text":"manual acceptance passed"}`))
	if err != nil || verified.Err != nil {
		t.Fatalf("verify err=%v res.Err=%v", err, verified.Err)
	}
	res, _ := NewGoalTool(svc).Execute(ctx, jsonRaw(t, `{"action":"mark_done"}`))
	if !strings.Contains(res.Text, "done") {
		t.Errorf("got %q", res.Text)
	}
	if svc.Active() != nil {
		t.Error("active should be cleared after mark_done")
	}
}

func TestGoalTool_MarkDoneRejectsMissingVerification(t *testing.T) {
	svc := newGoalTestService(t)
	ctx := context.Background()
	_, _ = svc.Set(ctx, "x", "", "", "")
	res, err := NewGoalTool(svc).Execute(ctx, jsonRaw(t, `{"action":"mark_done"}`))
	if err == nil || res.Err == nil || !strings.Contains(err.Error(), "verification required") {
		t.Fatalf("mark_done err=%v result=%+v", err, res)
	}
}

func TestGoalTool_VerifyRequiresEvidence(t *testing.T) {
	svc := newGoalTestService(t)
	_, _ = svc.Set(context.Background(), "x", "", "", "")
	_, err := NewGoalTool(svc).Execute(context.Background(), jsonRaw(t, `{"action":"verify","passed":true}`))
	if err == nil || !strings.Contains(err.Error(), "evidence") {
		t.Fatalf("verify error = %v", err)
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
