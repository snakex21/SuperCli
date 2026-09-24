package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestUnrelatedSuccessDoesNotResolveFailedCheck(t *testing.T) {
	reg := tools.NewRegistry()
	pass := false
	reg.MustRegister(tools.Tool{Name: "ctx_execute", Description: "fixture", Schema: `{"type":"object","properties":{"command":{"type":"array","items":{"type":"string"}},"timeout_ms":{"type":"integer","maximum":30000}},"required":["command"]}`, Fn: func(_ context.Context, raw json.RawMessage) (tools.Result, error) {
		if strings.Contains(string(raw), "test") && !pass {
			return tools.Result{Err: errors.New("test failed")}, nil
		}
		return tools.Result{Text: `{"exit_code":0}`}, nil
	}})
	reg.MustRegister(tools.Tool{Name: "read_lines", Description: "fixture", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		return tools.Result{Text: "source code"}, nil
	}})
	completed := 0
	reg.MustRegister(tools.Tool{Name: "goal", Description: "fixture", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		completed++
		return tools.Result{Text: "done"}, nil
	}})
	l, err := NewLoop(LoopConfig{Provider: echoProvider("test"), Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan Event, 32)
	invoke := func(name, args string) toolResult {
		return l.invoke(context.Background(), llm.ToolCall{ID: name, Name: name, Arguments: args}, events)
	}
	invoke("ctx_execute", `{"command":["go","test","./..."],"timeout_ms":120000}`)
	invoke("read_lines", "{}")
	invoke("ctx_execute", `{"command":["git","diff"]}`)
	if result := invoke("goal", `{"action":"complete_task"}`); !result.failed || completed != 0 {
		t.Fatal("unrelated successes hid failed tests")
	}
	pass = true
	invoke("ctx_execute", `{"command":["go","test","./other"],"timeout_ms":30000}`)
	if result := invoke("goal", `{"action":"verify","passed":true}`); !result.failed || completed != 0 {
		t.Fatal("different passing check hid unresolved failure")
	}
	invoke("ctx_execute", `{"command":["go","test","./..."],"timeout_ms":30000}`)
	if result := invoke("goal", `{"action":"verify","passed":true}`); result.failed || completed != 1 {
		t.Fatalf("successful corrected retry did not clear failure: %+v", result)
	}
}

func TestFailedCheckIdentityAndProcessCompletion(t *testing.T) {
	l := &Loop{baseDir: t.TempDir()}
	testArgs := `{"command":["go","test","./..."],"workdir":"pkg","timeout_ms":10000,"env_extra":[]}`
	call := llm.ToolCall{Name: "ctx_execute", Arguments: testArgs}
	l.recordCheckResult(call, tools.Result{Err: errors.New("tests failed")})
	l.recordCheckResult(llm.ToolCall{Name: "ctx_execute", Arguments: `{"command":["go","test","./..."],"workdir":"elsewhere"}`}, tools.Result{Text: "passed"})
	if !l.failedChecks.unresolved() {
		t.Fatal("another workspace cleared failure")
	}
	l.recordCheckResult(call, tools.Result{Text: "passed"})
	if l.failedChecks.unresolved() {
		t.Fatal("same check did not recover")
	}
	commandFailure := `{"command":["go","test","./..."],"workdir":"pkg","status":"failed","exit_code":1}`
	l.recordCheckResult(llm.ToolCall{Name: "process_session", Arguments: `{"action":"poll","id":"proc-1"}`}, tools.Result{Text: commandFailure, Err: errors.New("tests failed")})
	if !l.failedChecks.unresolved() {
		t.Fatal("background failure was not retained")
	}
	l.recordCheckResult(llm.ToolCall{Name: "process_session", Arguments: `{"action":"stop","id":"proc-1"}`}, tools.Result{Text: commandFailure})
	if !l.failedChecks.unresolved() {
		t.Fatal("stopping an already-failed process counted as a passing rerun")
	}
	l.recordCheckResult(llm.ToolCall{Name: "process_session", Arguments: `{"action":"start"}`}, tools.Result{Text: strings.Replace(commandFailure, "failed", "running", 1)})
	if !l.failedChecks.unresolved() {
		t.Fatal("running process counted as a passing check")
	}
	l.recordCheckResult(call, tools.Result{Text: "passed"})
	if l.failedChecks.unresolved() {
		t.Fatal("foreground rerun of failed background check did not recover")
	}
	l.recordCheckResult(call, tools.Result{Err: errors.New("failed")})
	l.failedChecks.reset()
	if l.failedChecks.unresolved() {
		t.Fatal("new turn retained stale guard")
	}
}

func TestVerificationCommandDetection(t *testing.T) {
	for _, command := range [][]string{{"pytest"}, {"ctest"}, {"go", "test", "./..."}, {"go", "build", "./..."}, {"npm.cmd", "run", "test"}, {"cargo", "check"}, {"python", "-m", "unittest"}, {"zig", "build", "test"}} {
		if !isVerificationCommand(command) {
			t.Errorf("check not recognized: %q", command)
		}
	}
	for _, command := range [][]string{nil, {"git", "diff"}, {"git", "status"}, {"cmd", "/c", "go test ./..."}, {"python", "script.py"}, {"npm", "install"}} {
		if isVerificationCommand(command) {
			t.Errorf("non-check inferred as verification: %q", command)
		}
	}
}
