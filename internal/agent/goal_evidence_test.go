package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestPassingGoalVerificationDetection(t *testing.T) {
	if !isPassingGoalVerification("goal", json.RawMessage(`{"action":"verify","passed":true,"text":"tests passed"}`)) {
		t.Fatal("passing goal verification was not detected")
	}
	for _, raw := range []string{`{"action":"verify","passed":false}`, `{"action":"show"}`, `{bad`} {
		if isPassingGoalVerification("goal", json.RawMessage(raw)) {
			t.Fatalf("non-passing args detected: %s", raw)
		}
	}
	if isPassingGoalVerification("ctx_execute", json.RawMessage(`{"passed":true}`)) {
		t.Fatal("non-goal tool detected")
	}
}

func TestGoalTaskCompletionDetection(t *testing.T) {
	if !isGoalTaskCompletion("goal", json.RawMessage(`{"action":"complete_task","task_seq":1}`)) {
		t.Fatal("goal task completion was not detected")
	}
	for _, raw := range []string{`{"action":"start_task"}`, `{"action":"verify","passed":true}`, `{bad`} {
		if isGoalTaskCompletion("goal", json.RawMessage(raw)) {
			t.Fatalf("non-completion args detected: %s", raw)
		}
	}
	if isGoalTaskCompletion("complete_task", json.RawMessage(`{"task_seq":1}`)) {
		t.Fatal("standalone action name detected as goal tool")
	}
}

func TestConcreteEvidenceToolExcludesMetaActions(t *testing.T) {
	for _, name := range []string{"goal", "tool_search", "recall", "ask_user"} {
		if isConcreteEvidenceTool(name) {
			t.Errorf("meta tool %q counted as evidence", name)
		}
	}
	for _, name := range []string{"ctx_execute", "read_image", "read_lines", "task"} {
		if !isConcreteEvidenceTool(name) {
			t.Errorf("concrete tool %q not counted as evidence", name)
		}
	}
}

func TestGoalCompletionBlockedUntilRecoveryAfterConcreteFailure(t *testing.T) {
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "ctx_execute", Description: "run tests", Schema: `{}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		return tools.Result{Err: errors.New("tests failed")}, nil
	}})
	reg.MustRegister(tools.Tool{Name: "edit_line", Description: "edit a line", Schema: `{}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		return tools.Result{Text: "edited"}, nil
	}})
	goalCalls := 0
	reg.MustRegister(tools.Tool{Name: "goal", Description: "manage goal", Schema: `{}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		goalCalls++
		return tools.Result{Text: "task complete"}, nil
	}})
	l := &Loop{registry: reg}
	out := make(chan Event, 16)

	failed := l.invoke(context.Background(), llm.ToolCall{ID: "test", Name: "ctx_execute", Arguments: `{}`}, out)
	if !failed.failed || !l.concreteFailure.Load() {
		t.Fatal("failed concrete tool did not set the completion guard")
	}
	blocked := l.invoke(context.Background(), llm.ToolCall{ID: "goal-1", Name: "goal", Arguments: `{"action":"complete_task","task_seq":1}`}, out)
	if !blocked.failed || goalCalls != 0 {
		t.Fatalf("goal completion was not blocked: failed=%v calls=%d", blocked.failed, goalCalls)
	}
	recovered := l.invoke(context.Background(), llm.ToolCall{ID: "edit", Name: "edit_line", Arguments: `{}`}, out)
	if recovered.failed || l.concreteFailure.Load() {
		t.Fatal("successful concrete tool did not clear the completion guard")
	}
	completed := l.invoke(context.Background(), llm.ToolCall{ID: "goal-2", Name: "goal", Arguments: `{"action":"complete_task","task_seq":1}`}, out)
	if completed.failed || goalCalls != 1 {
		t.Fatalf("goal completion remained blocked after recovery: failed=%v calls=%d", completed.failed, goalCalls)
	}
}
