package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/storage/goal"
)

func TestGoalTool_AddTaskExplicitTitlesThroughRegistry(t *testing.T) {
	ctx := context.Background()
	svc := newGoalTestService(t)
	if _, err := svc.Set(ctx, "Synthetic objective", "", "", ""); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.MustRegister(NewGoalTool(svc).Spec())
	res, err := reg.Execute(ctx, "goal", json.RawMessage(`{"action":"add_task","titles":["Inspect","Implement","Verify"]}`))
	if err != nil || res.Err != nil {
		t.Fatalf("batch err=%v res=%+v", err, res)
	}
	for _, line := range []string{"added 3 tasks:", "1. Inspect", "2. Implement", "3. Verify"} {
		if !strings.Contains(res.Text, line) {
			t.Fatalf("response omitted %q: %s", line, res.Text)
		}
	}
	tasks, err := svc.ListTasks(ctx, "")
	if err != nil || len(tasks) != 3 {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
	for i, task := range tasks {
		if task.Seq != i+1 || task.Status != goal.TaskPending {
			t.Fatalf("batch task %+v", task)
		}
	}
	res, err = reg.Execute(ctx, "goal", json.RawMessage(`{"action":"add_task","title":"Fourth"}`))
	if err != nil || res.Err != nil || res.Text != "added task 4: Fourth" {
		t.Fatalf("single call contract changed: %s %v %v", res.Text, err, res.Err)
	}
}

func TestGoalTool_AddTaskRejectsAmbiguousAndInvalidListsBeforeWriting(t *testing.T) {
	tooMany := make([]string, goal.MaxTaskBatch+1)
	for i := range tooMany {
		tooMany[i] = "synthetic task"
	}
	oversized, _ := json.Marshal(map[string]any{"action": "add_task", "titles": tooMany})
	for _, raw := range []string{
		`{"action":"add_task","title":"single","titles":["first","second"]}`,
		`{"action":"add_task","title":"","titles":["first"]}`,
		`{"action":"add_task","titles":null}`,
		`{"action":"add_task","titles":[]}`,
		`{"action":"add_task","titles":["first"," \t"]}`,
		`{"action":"add_task","titles":["first",42]}`,
		`{"action":"add_task","titles":["first",null]}`,
		`{"action":"add_task","titles":"first"}`,
		`{"action":"add_task","title":42,"titles":["first"]}`,
		`{"action":"add_task","title":false}`,
		`{"action":"set","title":"replacement","titles":["first"]}`,
		string(oversized),
	} {
		t.Run(fmt.Sprint(len(raw)), func(t *testing.T) {
			ctx := context.Background()
			svc := newGoalTestService(t)
			g, err := svc.Set(ctx, "Original", "", "", "")
			if err != nil {
				t.Fatal(err)
			}
			if err := svc.Verify(ctx, "", true, "fixture checked"); err != nil {
				t.Fatal(err)
			}
			tool := NewGoalTool(svc)
			// Runtime validation also protects callers that bypass registry schema.
			res, err := tool.Execute(ctx, json.RawMessage(raw))
			if err == nil || res.Err == nil {
				t.Fatalf("invalid list accepted: %+v %v", res, err)
			}
			if active := svc.Active(); active == nil || active.ID != g.ID || active.VerificationStatus != goal.VerificationPassed {
				t.Fatalf("invalid list changed active state: %+v", active)
			}
			if tasks, err := svc.ListTasks(ctx, ""); err != nil || len(tasks) != 0 {
				t.Fatalf("partial batch: %+v %v", tasks, err)
			}
			reg := NewRegistry()
			reg.MustRegister(tool.Spec())
			res, err = reg.Execute(ctx, "goal", json.RawMessage(raw))
			if err == nil && res.Err == nil {
				t.Fatalf("registry accepted invalid list: %+v", res)
			}
			if active := svc.Active(); active == nil || active.ID != g.ID || active.VerificationStatus != goal.VerificationPassed {
				t.Fatalf("registry rejection changed active state: %+v", active)
			}
			if tasks, err := svc.ListTasks(ctx, ""); err != nil || len(tasks) != 0 {
				t.Fatalf("registry left a partial batch: %+v %v", tasks, err)
			}
		})
	}
}

func TestGoalTool_AddTaskTreatsUnusedNullChoiceAsAbsent(t *testing.T) {
	for _, tc := range []struct {
		name, raw, response string
		count               int
	}{
		{"null single choice", `{"action":"add_task","title":null,"titles":["First","Second"]}`, "added 2 tasks:", 2},
		{"null batch choice", `{"action":"add_task","title":"First","titles":null}`, "added task 1: First", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, registry := range []bool{false, true} {
				ctx := context.Background()
				svc := newGoalTestService(t)
				if _, err := svc.Set(ctx, "Synthetic objective", "", "", ""); err != nil {
					t.Fatal(err)
				}
				tool := NewGoalTool(svc)
				execute := tool.Execute
				if registry {
					reg := NewRegistry()
					reg.MustRegister(tool.Spec())
					execute = func(ctx context.Context, args json.RawMessage) (Result, error) { return reg.Execute(ctx, "goal", args) }
				}
				res, err := execute(ctx, json.RawMessage(tc.raw))
				if err != nil || res.Err != nil || !strings.Contains(res.Text, tc.response) {
					t.Fatalf("registry=%t null choice rejected: %s %v %v", registry, res.Text, err, res.Err)
				}
				if tasks, err := svc.ListTasks(ctx, ""); err != nil || len(tasks) != tc.count {
					t.Fatalf("null choice changed meaning: %+v %v", tasks, err)
				}
			}
		})
	}
}

func TestGoalTool_AddTaskExplicitListUsesReturnedExistingSequence(t *testing.T) {
	ctx := context.Background()
	svc := newGoalTestService(t)
	if _, err := svc.Set(ctx, "Synthetic objective", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddTask(ctx, "", "Existing"); err != nil {
		t.Fatal(err)
	}
	tool := NewGoalTool(svc)
	res, err := tool.Execute(ctx, json.RawMessage(`{"action":"add_task","titles":["Second","Third"]}`))
	if err != nil || res.Err != nil || !strings.Contains(res.Text, "2. Second") || !strings.Contains(res.Text, "3. Third") {
		t.Fatalf("misleading task numbering: %s %v %v", res.Text, err, res.Err)
	}
	res, err = tool.Execute(ctx, json.RawMessage(`{"action":"start_task","task_seq":2}`))
	if err != nil || res.Err != nil {
		t.Fatalf("start returned task: %v %v", err, res.Err)
	}
	tasks, err := svc.ListTasks(ctx, "")
	if err != nil || len(tasks) != 3 || tasks[1].Title != "Second" || tasks[1].Status != goal.TaskInProgress || tasks[2].Status != goal.TaskPending {
		t.Fatalf("start task targeted wrong row: %+v %v", tasks, err)
	}
}
