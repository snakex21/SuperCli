package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/ctxexec"
)

func checkArgsFixture(t *testing.T) (*Loop, *bool, *int) {
	t.Helper()
	reg := tools.NewRegistry()
	passing, completions := false, 0
	spec := tools.NewCtxExecuteTool(nil, t.TempDir()).Spec()
	spec.Fn = func(_ context.Context, raw json.RawMessage) (tools.Result, error) {
		var args struct {
			Command []string
			Env     []string `json:"env_extra"`
		}
		if err := json.Unmarshal(raw, &args); err != nil || len(args.Command) != 3 {
			t.Fatalf("executor did not receive normalized argv: %s (%v)", raw, err)
		}
		if !passing {
			return tools.Result{Err: errors.New("test failed")}, nil
		}
		return tools.Result{Text: "{\"exit_code\":0,\"stdout\":\"PASS\",\"stderr\":\"\"}"}, nil
	}
	reg.MustRegister(spec)
	reg.MustRegister(tools.Tool{Name: "goal", Description: "fixture", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		completions++
		return tools.Result{Text: "done"}, nil
	}})
	l, err := NewLoop(LoopConfig{Provider: echoProvider("test"), Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	return l, &passing, &completions
}

func encodedCheckArgs(commandString, envString bool, mode string) string {
	var command any = []string{"go", "test", "./..."}
	var env any = []string{"CHECK_MODE=" + mode}
	if commandString {
		encoded, _ := json.Marshal(command)
		command = string(encoded)
	}
	if envString {
		encoded, _ := json.Marshal(env)
		env = string(encoded)
	}
	raw, _ := json.Marshal(map[string]any{"command": command, "env_extra": env})
	return string(raw)
}

func TestFailedCheckRecoveryAcceptsExecutionArgumentForms(t *testing.T) {
	for _, commandString := range []bool{false, true} {
		for _, envString := range []bool{false, true} {
			for _, encodedFailure := range []bool{false, true} {
				t.Run(fmt.Sprintf("command=%v/env=%v/failure=%v", commandString, envString, encodedFailure), func(t *testing.T) {
					l, passing, completions := checkArgsFixture(t)
					invoke := func(name, args string) toolResult {
						return l.invoke(context.Background(), llm.ToolCall{ID: name, Name: name, Arguments: args}, make(chan Event, 8))
					}
					plain, encoded := encodedCheckArgs(false, false, "full"), encodedCheckArgs(commandString, envString, "full")
					failure, retry := plain, encoded
					if encodedFailure {
						failure, retry = encoded, plain
					}
					if got := invoke("ctx_execute", failure); !got.failed || !l.failedChecks.unresolved() {
						t.Fatal("an executed failing check was not retained")
					}
					*passing = true
					if got := invoke("ctx_execute", encodedCheckArgs(commandString, envString, "quick")); got.failed {
						t.Fatalf("different environment did not execute: %+v", got)
					}
					if !l.failedChecks.unresolved() {
						t.Fatal("another environment hid the failed check")
					}
					if got := invoke("ctx_execute", retry); got.failed {
						t.Fatalf("retry failed: %+v", got)
					}
					if got := invoke("goal", "{\"action\":\"complete_task\"}"); got.failed || *completions != 1 {
						t.Fatalf("successful equivalent retry still required more work: %+v", got)
					}
				})
			}
		}
	}
}

func TestMalformedCheckArgumentsCannotClearFailure(t *testing.T) {
	l, passing, _ := checkArgsFixture(t)
	call := func(raw string) toolResult {
		return l.invoke(context.Background(), llm.ToolCall{ID: "test", Name: "ctx_execute", Arguments: raw}, make(chan Event, 8))
	}
	call(encodedCheckArgs(false, false, "full"))
	*passing = true
	for _, raw := range []string{
		"{\"command\":\"go test ./...\"}",
		"{\"command\":\"[\\\"go\\\",42]\"}",
		"{\"command\":[\"go\",\"test\",\"./...\"],\"env_extra\":\"CHECK_MODE=full\"}",
	} {
		if got := call(raw); !got.failed || !l.failedChecks.unresolved() {
			t.Fatalf("invalid arguments changed verification state: %s", raw)
		}
	}
}

func TestCheckRecoveryAcrossNativeAndTextTools(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprint(thin), func(t *testing.T) {
			l, passing, completions := checkArgsFixture(t)
			spec, _ := l.registry.Get("ctx_execute")
			execute := spec.Fn
			executions := 0
			reg := tools.NewRegistry()
			spec.Fn = func(ctx context.Context, raw json.RawMessage) (tools.Result, error) {
				executions++
				*passing = executions > 1
				return execute(ctx, raw)
			}
			reg.MustRegister(spec)
			goal, _ := l.registry.Get("goal")
			reg.MustRegister(goal)
			first := llm.ToolCall{ID: "fail", Name: "ctx_execute", Arguments: encodedCheckArgs(false, false, "full")}
			second := llm.ToolCall{ID: "pass", Name: "ctx_execute", Arguments: encodedCheckArgs(true, true, "full")}
			done := llm.ToolCall{ID: "done", Name: "goal", Arguments: "{\"action\":\"complete_task\"}"}
			scripts := [][]llm.Delta{
				{{ToolCall: &first}},
				{{ToolCall: &second}, {ToolCall: &done}},
				{{Content: "Done.", FinishReason: "stop"}},
			}
			if thin {
				scripts[1] = []llm.Delta{{Content: "«ctx_execute\ncommand: [\"go\",\"test\",\"./...\"]\nenv_extra: [\"CHECK_MODE=full\"]»\n«goal\naction: complete_task»", FinishReason: "stop"}}
			}
			provider := &stubProvider{name: "check-argument-replay", scripts: scripts}
			loop, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, ThinTools: thin, MaxSteps: 4})
			if err != nil {
				t.Fatal(err)
			}
			drainEvents(t, mustRun(t, loop, "Retry the test and complete after it passes."))
			if executions != 2 || *completions != 1 || loop.failedChecks.unresolved() {
				t.Fatalf("executions=%d completions=%d unresolved=%v", executions, *completions, loop.failedChecks.unresolved())
			}
		})
	}
}

func TestProcessCheckRecoveryAcceptsEncodedArguments(t *testing.T) {
	root := t.TempDir()
	reg := tools.NewRegistry()
	pass := false
	reg.MustRegister(tools.Tool{
		Name: "process_session", Description: "process fixture",
		Schema: `{"type":"object","properties":{"action":{"type":"string"},"command":{"type":"array","items":{"type":"string"}},"env":{"type":"array","items":{"type":"string"}}},"required":["action","command"]}`,
		Fn: func(_ context.Context, raw json.RawMessage) (tools.Result, error) {
			var args struct {
				Command []string
				Env     []string
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				t.Fatal(err)
			}
			status := "failed"
			var failure error = errors.New("tests failed")
			if pass {
				status, failure = "done", nil
			}
			body, _ := json.Marshal(map[string]any{"id": "proc-fixture", "command": args.Command, "workdir": root, "status": status})
			// Custom/legacy start result: no CommandKey, so the start args supply env.
			return tools.Result{Text: string(body), Err: failure}, nil
		},
	})
	l := &Loop{registry: reg, baseDir: root}
	invoke := func(encoded bool, mode string) {
		var command any = []string{"go", "test", "./..."}
		var env any = []string{"CHECK_MODE=" + mode}
		if encoded {
			c, _ := json.Marshal(command)
			command = string(c)
			e, _ := json.Marshal(env)
			env = string(e)
		}
		raw, _ := json.Marshal(map[string]any{"action": "start", "command": command, "env": env})
		l.invoke(context.Background(), llm.ToolCall{ID: "process", Name: "process_session", Arguments: string(raw)}, make(chan Event, 8))
	}
	invoke(true, "full")
	if !l.failedChecks.unresolved() {
		t.Fatal("encoded process failure lost")
	}
	pass = true
	invoke(false, "quick")
	if !l.failedChecks.unresolved() {
		t.Fatal("another process environment hid failure")
	}
	invoke(false, "full")
	if l.failedChecks.unresolved() {
		t.Fatal("equivalent process retry stayed unresolved")
	}
}

func TestRealCheckRecoveryWithEncodedRetry(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("Go executable unavailable")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module checkfixture\n\ngo 1.22\n"), 0600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "fixture_test.go")
	writeTest := func(body string) {
		t.Helper()
		if err := os.WriteFile(source, []byte("package checkfixture\nimport \"testing\"\nfunc TestFixture(t *testing.T){"+body+"}\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeTest("t.Fatal(\"fixture failure\")")
	reg := tools.NewRegistry()
	spec := tools.NewCtxExecuteTool(ctxexec.New(root), root).Spec()
	run := spec.Fn
	executions := 0
	spec.Fn = func(ctx context.Context, raw json.RawMessage) (tools.Result, error) {
		executions++
		return run(ctx, raw)
	}
	reg.MustRegister(spec)
	completions := 0
	reg.MustRegister(tools.Tool{Name: "goal", Description: "fixture", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		completions++
		return tools.Result{Text: "done"}, nil
	}})
	l, err := NewLoop(LoopConfig{Provider: echoProvider("test"), Registry: reg, BaseDir: root})
	if err != nil {
		t.Fatal(err)
	}
	call := func(encoded bool) toolResult {
		var command any = []string{"go", "test", "-count=1", "."}
		var env any = []string{"GOWORK=off", "GOPROXY=off", "GOSUMDB=off"}
		if encoded {
			c, _ := json.Marshal(command)
			command = string(c)
			e, _ := json.Marshal(env)
			env = string(e)
		}
		raw, _ := json.Marshal(map[string]any{"command": command, "env_extra": env, "timeout_ms": 30000})
		return l.invoke(context.Background(), llm.ToolCall{ID: "test", Name: "ctx_execute", Arguments: string(raw)}, make(chan Event, 8))
	}
	if got := call(false); !got.failed || !l.failedChecks.unresolved() {
		t.Fatalf("expected a real failed check: %+v", got)
	}
	writeTest("")
	if got := call(true); got.failed {
		t.Fatalf("real corrected retry failed: %+v", got)
	}
	done := l.invoke(context.Background(), llm.ToolCall{ID: "done", Name: "goal", Arguments: "{\"action\":\"complete_task\"}"}, make(chan Event, 8))
	if done.failed || executions != 2 || completions != 1 || l.failedChecks.unresolved() {
		t.Fatalf("successful test required another run: executions=%d completions=%d result=%+v", executions, completions, done)
	}
}
