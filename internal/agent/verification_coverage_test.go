package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/core"
)

func executionFixture(root string, command []string, stdout string, exit int) (llm.ToolCall, tools.Result) {
	raw, _ := json.Marshal(map[string]any{"command": command, "workdir": root})
	result := tools.Result{Text: `{"exit_code":0}`, Execution: &core.ExecutionEvidence{
		Command: command, Workdir: root, Environment: core.CommandKey(nil, root, nil), StartedAt: time.Now(),
		StartedSequence: core.NextExecutionSequence(),
		Completed:       true, ExitCode: exit, OutputComplete: true, Stdout: stdout,
	}}
	if exit != 0 {
		result.Err = errors.New("executor: failed assertion")
	}
	return llm.ToolCall{Name: "ctx_execute", Arguments: string(raw)}, result
}

func TestVerificationCoveringSuiteResolvesActualFailedPackage(t *testing.T) {
	root := t.TempDir()
	l := &Loop{baseDir: root}
	failure, failed := executionFixture(root, []string{"go", "test", "./pkg", "-run", "^TestValue$"}, "--- FAIL: TestValue (0.00s)\nFAIL\tfixture/pkg\t0.1s\n", 1)
	l.recordCheckResult(failure, failed)
	pass, passed := executionFixture(root, []string{"go", "test", "./...", "-v", "-count=1", "-timeout=1m"}, "--- PASS: TestValue (0.00s)\nPASS\nok\tfixture/pkg\t0.1s\n?\tfixture/cmd\t[no test files]\n", 0)
	l.recordCheckResult(pass, passed)
	if l.failedChecks.unresolved() {
		t.Fatal("a proven compatible broader suite still requires an exact rerun")
	}
	t.Log("failed package + broader suite: 2 executor operations, 0 redundant exact reruns")
}

func TestVerificationCoverageKeepsModeScopeAndActualPackage(t *testing.T) {
	for _, variant := range []string{"race", "tags", "environment", "same command environment", "root", "package", "unreported package", "filtered", "unknown flags", "fewer repetitions", "shell wrapper"} {
		t.Run(variant, func(t *testing.T) {
			root := t.TempDir()
			l := &Loop{baseDir: root}
			failCommand := []string{"go", "test", "./pkg"}
			passCommand := []string{"go", "test", "./..."}
			if variant == "race" {
				failCommand = append(failCommand, "-race")
			}
			if variant == "tags" {
				failCommand = append(failCommand, "-tags=integration")
			}
			if variant == "fewer repetitions" {
				failCommand = append(failCommand, "-count=3")
			}
			if variant == "shell wrapper" {
				passCommand = []string{"sh", "-lc", "go test ./..."}
			}
			if variant == "package" {
				passCommand = []string{"go", "test", "./other"}
			}
			if variant == "filtered" {
				passCommand = append(passCommand, "-run=Other")
			}
			if variant == "unknown flags" {
				passCommand = append(passCommand, "-test.unknown=on")
			}
			failure, failed := executionFixture(root, failCommand, "FAIL\tfixture/pkg\t0.1s\n", 1)
			pass, passed := executionFixture(root, passCommand, "ok\tfixture/pkg\t0.1s\n", 0)
			if variant == "same command environment" {
				pass, passed = executionFixture(root, failCommand, "ok fixture/pkg 0.1s", 0)
			}
			if variant == "environment" || variant == "same command environment" {
				passed.Execution.Environment = core.CommandKey(nil, root, []string{"MODE=other"})
			}
			if variant == "root" {
				passed.Execution.Workdir = t.TempDir()
			}
			if variant == "unreported package" {
				passed.Execution.Stdout = "ok\tfixture/other\t0.1s\n"
			}
			l.recordCheckResult(failure, failed)
			l.recordCheckResult(pass, passed)
			if !l.failedChecks.unresolved() {
				t.Fatal("incompatible or missing coverage cleared the failure")
			}
		})
	}
}

func TestVerificationSkipNotRunOrTextClaimCannotPass(t *testing.T) {
	for _, variant := range []string{"text PASS", "worker claim", "not run", "no tests", "skipped", "truncated"} {
		t.Run(variant, func(t *testing.T) {
			root := t.TempDir()
			l := &Loop{baseDir: root}
			command := []string{"go", "test", "./pkg"}
			call, fail := executionFixture(root, command, "FAIL\tfixture/pkg\t0.1s\n", 1)
			l.recordCheckResult(call, fail)
			pass, result := executionFixture(root, command, "ok\tfixture/pkg\t0.1s\n", 0)
			switch variant {
			case "text PASS":
				result = tools.Result{Text: "PASS"}
			case "worker claim":
				pass.Name = "task"
				result = tools.Result{Text: "All tests PASS", Execution: result.Execution}
			case "not run":
				result.Execution.Completed = false
			case "no tests":
				result.Execution.Stdout = "testing: warning: no tests to run\nok fixture/pkg 0.1s [no tests to run]"
			case "skipped":
				result.Execution.Stdout = "--- SKIP: TestValue (0.00s)\nPASS\nok fixture/pkg 0.1s"
			case "truncated":
				result.Execution.OutputComplete = false
			}
			l.recordCheckResult(pass, result)
			if !l.failedChecks.unresolved() {
				t.Fatal("non-execution or missing evidence counted as PASS")
			}
		})
	}
}

func TestVerificationPassExpiresAfterEditAndOldProcessCannotRefreshIt(t *testing.T) {
	root := t.TempDir()
	l := &Loop{baseDir: root}
	call, result := executionFixture(root, []string{"go", "test", "./pkg"}, "ok fixture/pkg 0.1s", 0)
	l.recordCheckResult(call, result)
	l.failedChecks.workspaceChanged()
	if !l.failedChecks.unresolved() {
		t.Fatal("old successful verification approved edited code")
	}
	l.recordCheckResult(call, result)
	if !l.failedChecks.unresolved() {
		t.Fatal("replaying the old terminal process approved newer code")
	}
	fresh, passed := executionFixture(root, []string{"go", "test", "./pkg"}, "ok fixture/pkg 0.1s", 0)
	l.recordCheckResult(fresh, passed)
	if l.failedChecks.unresolved() {
		t.Fatal("fresh successful executor rerun did not verify the edit")
	}
}

func TestVerificationGoalRetryUsesEvidenceStateNotRewordedClaim(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	passing, goalExecutions := false, 0
	registry.MustRegister(tools.Tool{Name: "ctx_execute", Description: "fixture", Schema: `{}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		code, stdout := 1, "FAIL fixture/pkg 0.1s"
		if passing {
			code, stdout = 0, "ok fixture/pkg 0.1s"
		}
		_, result := executionFixture(root, []string{"go", "test", "./pkg"}, stdout, code)
		return result, nil
	}})
	registry.MustRegister(tools.Tool{Name: "goal", Description: "fixture", Schema: `{}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		goalExecutions++
		return tools.Result{Text: "verified"}, nil
	}})
	l := &Loop{registry: registry, baseDir: root}
	invoke := func(name, args string) toolResult {
		return l.invoke(context.Background(), llm.ToolCall{ID: name, Name: name, Arguments: args}, make(chan Event, 16))
	}
	invoke("ctx_execute", `{"command":["go","test","./pkg"]}`)
	for _, prose := range []string{"PASS", "all green"} {
		if result := invoke("goal", `{"action":"verify","passed":true,"text":"`+prose+`"}`); !result.failed {
			t.Fatal("failure did not block verification")
		}
	}
	blocked := invoke("goal", `{"action":"verify","passed":true,"text":"new wording"}`)
	if !blocked.failed || !strings.Contains(blocked.followUps[0].Content, "already failed twice") {
		t.Fatal("unchanged state loop was not blocked")
	}
	passing = true
	invoke("ctx_execute", `{"command":["go","test","./pkg"]}`)
	if result := invoke("goal", `{"action":"verify","passed":true,"text":"PASS"}`); result.failed || goalExecutions != 1 {
		t.Fatalf("new executor evidence did not admit identical goal retry: failed=%v executions=%d", result.failed, goalExecutions)
	}
	t.Log("unchanged goal state: third claim blocked; new PASS: identical retry admitted without workaround")
}

func TestVerificationGoalScopeChangeRejectsPreviousWorkerEvidence(t *testing.T) {
	scope := "g-first"
	registry := tools.NewRegistry()
	registry.MustRegister(tools.Tool{Name: "goal", Description: "fixture", Schema: `{}`, VerificationState: func() core.VerificationState { return core.VerificationState{Scope: scope} }, Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }})
	l := &Loop{registry: registry}
	l.bindVerificationGoalScope()
	observe := l.failedChecks.observer()
	observe(verificationObservation{key: [32]byte{1}, failed: true, sequence: verificationSequence.Add(1)})
	scope = "g-second"
	l.bindVerificationGoalScope()
	observe(verificationObservation{key: [32]byte{2}, failed: true, sequence: verificationSequence.Add(1)})
	if l.failedChecks.unresolved() {
		t.Fatal("new goal inherited old goal/worker failures")
	}
}

func TestVerificationIdenticalGoalRetryAfterExecutorProgress(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	goalExecutions := 0
	registry.MustRegister(tools.Tool{Name: "goal", Description: "fixture", Schema: `{}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		goalExecutions++
		return tools.Result{Text: "verified"}, nil
	}})
	l := &Loop{registry: registry, baseDir: root}
	check, failed := executionFixture(root, []string{"go", "test", "./pkg"}, "FAIL fixture/pkg 0.1s", 1)
	l.recordCheckResult(check, failed)
	l.toolEvidence.Store(true)
	goal := llm.ToolCall{ID: "goal", Name: "goal", Arguments: `{"action":"verify","passed":true,"text":"tests passed"}`}
	for i := 0; i < 2; i++ {
		if result := l.invoke(context.Background(), goal, make(chan Event, 16)); !result.failed {
			t.Fatal("unresolved failure was ignored")
		}
	}
	passedCheck, passed := executionFixture(root, []string{"go", "test", "./pkg"}, "ok fixture/pkg 0.1s", 0)
	l.recordCheckResult(passedCheck, passed)
	if result := l.invoke(context.Background(), goal, make(chan Event, 16)); result.failed || goalExecutions != 1 {
		t.Fatalf("new successful executor evidence still requires changing goal arguments: failed=%v executions=%d", result.failed, goalExecutions)
	}
}

func TestVerificationGoalRevisionAllowsRetryButDoesNotClearFailedTests(t *testing.T) {
	state := core.VerificationState{Scope: "g-fixture", Total: 1}
	registry := tools.NewRegistry()
	goalExecutions := 0
	registry.MustRegister(tools.Tool{Name: "goal", Description: "fixture", Schema: `{}`,
		VerificationState: func() core.VerificationState { return state },
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			goalExecutions++
			if state.Done != state.Total {
				return tools.Result{Err: errors.New("open tasks")}, nil
			}
			return tools.Result{Text: "verified"}, nil
		}})
	l := &Loop{registry: registry, baseDir: t.TempDir()}
	l.toolEvidence.Store(true)
	goal := llm.ToolCall{ID: "goal", Name: "goal", Arguments: `{"action":"verify","passed":true,"text":"work complete"}`}
	invoke := func() toolResult { return l.invoke(context.Background(), goal, make(chan Event, 16)) }
	for i := 0; i < 2; i++ {
		if !invoke().failed {
			t.Fatal("open tasks were accepted")
		}
	}
	if !invoke().failed || goalExecutions != 2 {
		t.Fatal("unchanged goal failure loop was not blocked")
	}
	state.Done = state.Total // The real host/GUI completed its existing task.
	if invoke().failed || goalExecutions != 3 {
		t.Fatal("new cached goal revision did not permit the identical retry")
	}
	check, failed := executionFixture(l.baseDir, []string{"go", "test", "./pkg"}, "FAIL fixture/pkg 0.1s", 1)
	l.recordCheckResult(check, failed)
	state.Total++
	state.Done++
	if !invoke().failed || !l.failedChecks.unresolved() {
		t.Fatal("goal task revision hid an actual test failure")
	}
}
