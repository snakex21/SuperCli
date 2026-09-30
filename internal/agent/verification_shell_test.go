package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/core"
)

func TestWrappedFailedVerificationRequiresSuccessfulRerun(t *testing.T) {
	for _, command := range [][]string{
		{"cmd", "/d", "/c", "go test ./..."},
		{"cmd.exe", "/c", "go", "test", "./..."},
		{"powershell", "-NoProfile", "-Command", "go test ./..."},
		{"pwsh", "-NoLogo", "-NonInteractive", "-Command", "cargo check"},
		{"/bin/sh", "-c", "pytest"},
		{"bash", "-lc", "npm run test"},
	} {
		t.Run(strings.Join(command, " "), func(t *testing.T) {
			passing := false
			reg := tools.NewRegistry()
			reg.MustRegister(tools.Tool{Name: "ctx_execute", Description: "fixture", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				if !passing {
					return tools.Result{Err: errors.New("verification failed")}, nil
				}
				return tools.Result{Text: `{"exit_code":0}`}, nil
			}})
			reg.MustRegister(tools.Tool{Name: "read_lines", Description: "fixture", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				return tools.Result{Text: "source inspected"}, nil
			}})
			completed := 0
			reg.MustRegister(tools.Tool{Name: "goal", Description: "fixture", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				completed++
				return tools.Result{Text: "done"}, nil
			}})
			loop, err := NewLoop(LoopConfig{Provider: echoProvider("test"), Registry: reg, BaseDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			events := make(chan Event, 64)
			invoke := func(name, args string) toolResult {
				return loop.invoke(context.Background(), llm.ToolCall{ID: name, Name: name, Arguments: args}, events)
			}
			raw, _ := json.Marshal(map[string]any{"command": command})
			if result := invoke("ctx_execute", string(raw)); !result.failed {
				t.Fatal("fixture must fail")
			}
			invoke("read_lines", "{}")
			if result := invoke("goal", `{"action":"complete_task"}`); !result.failed || completed != 0 {
				t.Fatal("unrelated success hid failed wrapped check")
			}
			passing = true
			if result := invoke("ctx_execute", string(raw)); result.failed {
				t.Fatalf("rerun failed: %+v", result)
			}
			if result := invoke("goal", `{"action":"verify","passed":true}`); result.failed || completed != 1 {
				t.Fatalf("passing rerun did not recover: %+v", result)
			}
		})
	}
}

func TestShellVerificationDoesNotInferChecksFromScripts(t *testing.T) {
	for _, command := range [][]string{
		{"cmd", "/c", "echo go test ./..."},
		{"cmd", "/c", "go test ./... & echo ignored"},
		{"cmd", "/c", "%CHECK_COMMAND%"},
		{"cmd", "/k", "go test ./..."},
		{"powershell", "-Command", "go test ./...; exit 0"},
		{"powershell", "-Command", "& go test ./... | Out-String"},
		{"powershell", "-File", "test.ps1"},
		{"pwsh", "-Command", "Write-Output 'go test ./...'"},
		{"sh", "-c", "go test ./... || true"},
		{"bash", "-c", "printf 'pytest'"},
		{"bash", "-c", "go test ./...\necho masked"},
		{"sh", "-c", "go test ./...", "go test ./other"},
	} {
		if isVerificationCommand(command) {
			t.Errorf("script inferred as a single check: %q", command)
		}
	}
}

func TestWrappedCheckKeepsOriginalExecutionIdentity(t *testing.T) {
	loop := &Loop{baseDir: t.TempDir()}
	call := func(command []string, env []string) llm.ToolCall {
		raw, _ := json.Marshal(map[string]any{"command": command, "env_extra": env})
		return llm.ToolCall{Name: "ctx_execute", Arguments: string(raw)}
	}
	original := call([]string{"powershell", "-NoProfile", "-Command", "go test ./..."}, []string{"MODE=full"})
	loop.recordCheckResult(original, tools.Result{Err: errors.New("failed")})
	for _, other := range []llm.ToolCall{
		call([]string{"go", "test", "./..."}, []string{"MODE=full"}),
		call([]string{"powershell", "-NoProfile", "-Command", "go test ./other"}, []string{"MODE=full"}),
		call([]string{"powershell", "-NoProfile", "-Command", "go test ./..."}, []string{"MODE=quick"}),
	} {
		loop.recordCheckResult(other, tools.Result{Text: "passed"})
		if !loop.failedChecks.unresolved() {
			t.Fatal("different command or environment cleared wrapped failure")
		}
	}
	loop.recordCheckResult(original, tools.Result{Text: "passed"})
	if loop.failedChecks.unresolved() {
		t.Fatal("same execution did not resolve failure")
	}
}

func TestWrappedBackgroundVerificationUsesExecutionMetadata(t *testing.T) {
	loop := &Loop{baseDir: t.TempDir()}
	command := []string{"cmd", "/d", "/c", "go test ./..."}
	env := []string{"MODE=full"}
	key := core.CommandKey(command, loop.baseDir, env)
	raw, _ := json.Marshal(map[string]any{"id": "proc-shell", "command": command, "workdir": loop.baseDir, "status": "failed", "exit_code": 1})
	wait := llm.ToolCall{Name: "process_session", Arguments: `{"action":"wait","id":"proc-shell"}`}
	loop.recordCheckResult(wait, tools.Result{Text: string(raw), Err: errors.New("tests failed"), CommandKey: &key})
	if !loop.failedChecks.unresolved() {
		t.Fatal("wrapped background failure was not retained")
	}
	for _, status := range []string{"running", "done"} {
		otherKey := core.CommandKey(command, loop.baseDir, []string{"MODE=quick"})
		snap, _ := json.Marshal(map[string]any{"id": "proc-other", "command": command, "workdir": loop.baseDir, "status": status})
		loop.recordCheckResult(wait, tools.Result{Text: string(snap), CommandKey: &otherKey})
		if !loop.failedChecks.unresolved() {
			t.Fatal("unfinished or different process hid the failure")
		}
	}
	args, _ := json.Marshal(map[string]any{"command": command, "env_extra": env})
	loop.recordCheckResult(llm.ToolCall{Name: "ctx_execute", Arguments: string(args)}, tools.Result{Text: "passed"})
	if loop.failedChecks.unresolved() {
		t.Fatal("same foreground check did not resolve background failure")
	}
}

func TestVerificationShellNestingIsBounded(t *testing.T) {
	if !isVerificationCommand([]string{"cmd", "/c", "cmd /d /c go test ./..."}) {
		t.Fatal("simple nested command was not recognized")
	}
	nested := "go test ./..."
	for i := 0; i < 8; i++ {
		nested = "cmd /c " + nested
	}
	if isVerificationCommand([]string{"cmd", "/c", nested}) {
		t.Fatal("deep shell text was parsed as a check")
	}
}
