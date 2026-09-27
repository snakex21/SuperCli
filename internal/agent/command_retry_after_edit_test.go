package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/files"
)

// GunMayhem: the same live test failed, a patch fixed the panic, then a DNS
// error counted as the second failure. The gate refused the next test run.
func TestInvoke_CommandRetryAfterEdit(t *testing.T) {
	for _, command := range [][]string{
		{"go", "test", "./..."},
		{"powershell", "-NoProfile", "-Command", "$env:LIVE_TEST='1'; go test ./..."},
	} {
		t.Run(command[0], func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "source.txt")
			if err := os.WriteFile(target, []byte("broken\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			runs := 0
			reg := tools.NewRegistry()
			reg.MustRegister(files.NewPatchFile(dir).Spec())
			reg.MustRegister(tools.Tool{Name: "ctx_execute", Description: "check file", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				runs++
				data, err := os.ReadFile(target)
				if err != nil {
					return tools.Result{Err: err}, nil
				}
				if string(data) != "fixed\n" {
					return tools.Result{Err: errors.New("command_failed: broken source")}, nil
				}
				return tools.Result{Text: "{\"exit_code\":0}"}, nil
			}})
			reg.MustRegister(tools.Tool{Name: "read_context", Description: "read fixture", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				return tools.Result{Text: "source remains broken"}, nil
			}})
			l, err := NewLoop(LoopConfig{Provider: echoProvider("test"), Registry: reg, BaseDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			events := make(chan Event, 64)
			invoke := func(name string, args any) toolResult {
				raw, err := json.Marshal(args)
				if err != nil {
					t.Fatal(err)
				}
				return l.invoke(context.Background(), llm.ToolCall{ID: name, Name: name, Arguments: string(raw)}, events)
			}
			args := map[string]any{"command": command, "workdir": dir}
			assertBlocked := func() {
				t.Helper()
				result := invoke("ctx_execute", args)
				if !result.failed || len(result.followUps) == 0 || !strings.Contains(result.followUps[0].Content, "blocked:") || runs != 2 {
					t.Fatalf("unchanged failed command should stay blocked, runs=%d result=%+v", runs, result)
				}
			}
			for i := 0; i < 2; i++ {
				if result := invoke("ctx_execute", args); !result.failed {
					t.Fatal("broken check unexpectedly passed")
				}
			}
			assertBlocked()
			invoke("read_context", map[string]any{})
			assertBlocked()
			if result := invoke("patch_file", map[string]any{"path": "source.txt", "old": "missing", "new": "fixed"}); !result.failed {
				t.Fatal("invalid patch unexpectedly passed")
			}
			assertBlocked()
			if result := invoke("patch_file", map[string]any{"path": "source.txt", "old": "broken", "new": "broken"}); result.failed {
				t.Fatalf("no-op patch failed: %+v", result)
			}
			assertBlocked()
			if result := invoke("patch_file", map[string]any{"path": "source.txt", "old": "broken", "new": "fixed"}); result.failed {
				t.Fatalf("repair failed: %+v", result)
			}
			if command[0] == "go" && !l.failedChecks.unresolved() {
				t.Fatal("editing is not evidence of a passing test")
			}
			if result := invoke("ctx_execute", args); result.failed || runs != 3 {
				t.Fatalf("same check must execute after the repair, runs=%d result=%+v", runs, result)
			}
			if l.failedChecks.unresolved() {
				t.Fatal("successful rerun did not resolve the check")
			}
		})
	}
}

func TestCommandRepairPreservesOtherFailureGuards(t *testing.T) {
	var gate identicalFailureGate
	for _, name := range []string{"ctx_execute", "read_lines", "patch_file"} {
		gate.recordFailure(name, "{}")
		gate.recordFailure(name, "{}")
	}
	gate.workspaceChanged()
	if gate.shouldBlock("ctx_execute", "{}") {
		t.Fatal("commands must be allowed to check the edited workspace")
	}
	for _, name := range []string{"read_lines", "patch_file"} {
		if !gate.shouldBlock(name, "{}") {
			t.Fatalf("an unrelated edit cleared the failure guard for %s", name)
		}
	}
	gate.recordFailure("ctx_execute", "{}")
	if gate.shouldBlock("ctx_execute", "{}") {
		t.Fatal("a failure from before the edit survived")
	}
	gate.recordFailure("ctx_execute", "{}")
	if !gate.shouldBlock("ctx_execute", "{}") {
		t.Fatal("unchanged command failures must still be detected")
	}
}
