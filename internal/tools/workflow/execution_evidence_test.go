package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestCtxExecuteProvidesPrivateExecutorEvidence(t *testing.T) {
	command, ok := echoCmd("executor evidence")
	if !ok {
		t.Skip("no echo equivalent")
	}
	tool := NewCtxExecuteTool(newCtxTestRunner(t), ".")
	args := mustJSON(t, jsonObj(t, map[string]any{"command": command, "env_extra": []string{"PRIVATE_TEST_VALUE=hidden"}}))
	result, err := tool.Execute(context.Background(), args)
	if err != nil || result.Err != nil {
		t.Fatalf("%v %v", err, result.Err)
	}
	if result.Execution == nil || !result.Execution.Completed || result.Execution.ExitCode != 0 || !result.Execution.OutputComplete || result.Execution.StartedSequence == 0 || !strings.Contains(result.Execution.Stdout, "executor evidence") {
		t.Fatalf("missing actual executor result: %+v", result.Execution)
	}
	encoded, err := json.Marshal(result)
	if err != nil || strings.Contains(string(encoded), "StartedSequence") || strings.Contains(result.Text, "PRIVATE_TEST_VALUE") || strings.Contains(string(encoded), "hidden") {
		t.Fatal("private executor metadata leaked into output")
	}
}

func TestGoalVerificationScopeReflectsActualLifecycle(t *testing.T) {
	svc := newGoalTestService(t)
	tool := NewGoalTool(svc)
	identity := tool.Spec().VerificationState
	if identity == nil || identity().Scope != "" {
		t.Fatal("missing empty initial goal identity")
	}
	ctx := context.Background()
	first, err := svc.Set(ctx, "first", "", "", "")
	if err != nil || identity().Scope != first.ID {
		t.Fatal("goal identity did not follow actual host state")
	}
	if _, err := tool.Execute(ctx, json.RawMessage(`{"action":"abandon"}`)); err != nil || identity().Scope != "" {
		t.Fatal("abandoned goal remains an active verification scope")
	}
	second, err := svc.Set(ctx, "second", "", "", "")
	if err != nil || identity().Scope != second.ID || identity().Scope == first.ID {
		t.Fatal("another goal reused previous verification scope")
	}
}

func TestCtxExecuteEffectiveEnvironmentChangesEvidence(t *testing.T) {
	command, ok := echoCmd("environment evidence")
	if !ok {
		t.Skip("no echo equivalent")
	}
	tool := NewCtxExecuteTool(newCtxTestRunner(t), ".")
	args := mustJSON(t, jsonObj(t, map[string]any{"command": command}))
	t.Setenv("GOFLAGS", "-tags=fixture_first")
	first, err := tool.Execute(context.Background(), args)
	if err != nil || first.Err != nil || first.Execution == nil {
		t.Fatal("first executor result missing")
	}
	t.Setenv("GOFLAGS", "-tags=fixture_second")
	second, err := tool.Execute(context.Background(), args)
	if err != nil || second.Err != nil || second.Execution == nil {
		t.Fatal("second executor result missing")
	}
	if first.Execution.Environment == second.Execution.Environment {
		t.Fatal("inherited GOFLAGS was omitted from execution identity")
	}
}
