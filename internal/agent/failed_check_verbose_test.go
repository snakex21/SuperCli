package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/ctxexec"
	"supercli/internal/tools/processsession"
)

func verboseRetryFixture(t *testing.T) (string, *tools.Registry, *int) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("Go unavailable")
	}
	root := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":        "module verbosefixture\n\ngo 1.22\n",
		"value.go":      "package verbosefixture\nfunc Valid() bool { return false }\n",
		"value_test.go": "package verbosefixture\nimport \"testing\"\nfunc TestValid(t *testing.T) { if !Valid() { t.Fatal(\"value is invalid\") } }\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.NewCtxExecuteTool(ctxexec.New(root), root).Spec())
	reg.MustRegister(tools.NewPatchFile(root).Spec())
	completed := new(int)
	reg.MustRegister(tools.Tool{Name: "goal", Description: "record completion", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		*completed++
		return tools.Result{Text: "completed"}, nil
	}})
	return root, reg, completed
}

func verboseCheckCall(id string, verbose bool) llm.ToolCall {
	command := []string{"go", "test", "./..."}
	if verbose {
		command = append(command, "-v")
	}
	args, _ := json.Marshal(map[string]any{"command": command, "env_extra": []string{"GOWORK=off", "GOPROXY=off", "GOSUMDB=off"}, "timeout_ms": 30000})
	return llm.ToolCall{ID: id, Name: "ctx_execute", Arguments: string(args)}
}

func TestGoVerboseRetryCompletesGoal(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, verboseFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("thin=%v/verboseFirst=%v", thin, verboseFirst), func(t *testing.T) {
				root, reg, completed := verboseRetryFixture(t)
				first := verboseCheckCall("failed-check", verboseFirst)
				second := verboseCheckCall("passed-check", !verboseFirst)
				patch := llm.ToolCall{ID: "fix", Name: "patch_file", Arguments: `{"path":"value.go","old":"return false","new":"return true"}`}
				done := llm.ToolCall{ID: "finish", Name: "goal", Arguments: `{"action":"complete_task"}`}
				scripts := [][]llm.Delta{{{ToolCall: &first}}, {{ToolCall: &patch}}, {{ToolCall: &second}}, {{ToolCall: &done}}, {{Content: "Done.", FinishReason: "stop"}}}
				if thin {
					for i, call := range []llm.ToolCall{first, patch, second, done} {
						var args map[string]json.RawMessage
						if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
							t.Fatal(err)
						}
						keys := make([]string, 0, len(args))
						for key := range args {
							keys = append(keys, key)
						}
						sort.Strings(keys)
						text := "«" + call.Name
						for _, key := range keys {
							value := string(args[key])
							var plain string
							if json.Unmarshal(args[key], &plain) == nil {
								value = plain
							}
							text += "\n" + key + ": " + value
						}
						scripts[i] = []llm.Delta{{Content: text + "»", FinishReason: "stop"}}
					}
				}
				loop, err := NewLoop(LoopConfig{Provider: &stubProvider{name: "verbose-retry", scripts: scripts}, Registry: reg, BaseDir: root, ThinTools: thin, MaxSteps: 6})
				if err != nil {
					t.Fatal(err)
				}
				drainEvents(t, mustRun(t, loop, "Fix the failing production code, rerun its tests and complete the goal."))
				failed, passed := false, false
				for _, m := range loop.Messages {
					if m.Role == llm.RoleTool && m.Name == "ctx_execute" {
						failed = failed || strings.Contains(m.Content, "value is invalid")
						passed = passed || strings.Contains(m.Content, `"exit_code":0`)
					}
				}
				if !failed || !passed {
					for _, m := range loop.Messages {
						if m.Role == llm.RoleTool {
							t.Logf("%s: %s", m.Name, m.Content)
						}
					}
					t.Fatalf("fixture did not execute real failure and success: failed=%v passed=%v", failed, passed)
				}
				if *completed != 1 || loop.failedChecks.unresolved() {
					t.Fatalf("successful verbose retry blocked completion: completed=%d unresolved=%v", *completed, loop.failedChecks.unresolved())
				}
			})
		}
	}
}

func TestGoVerboseRetryAcrossManagedProcess(t *testing.T) {
	for _, managedFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(managedFirst), func(t *testing.T) {
			root, reg, completed := verboseRetryFixture(t)
			process := processsession.New(root)
			defer process.Close()
			reg.MustRegister(process.Spec())
			loop, err := NewLoop(LoopConfig{Provider: echoProvider("test"), Registry: reg, BaseDir: root})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			call := func(tc llm.ToolCall) toolResult { return loop.invoke(ctx, tc, make(chan Event, 32)) }
			managed := func() toolResult {
				args := `{"action":"start","command":["go","test","./...","-v"],"env":["GOWORK=off","GOPROXY=off","GOSUMDB=off"],"yield_ms":0}`
				started := call(llm.ToolCall{ID: "start", Name: "process_session", Arguments: args})
				if started.failed {
					t.Fatalf("start: %+v", started)
				}
				var snap struct {
					ID string `json:"id"`
				}
				if len(started.followUps) != 1 {
					t.Fatal("missing process start result")
				}
				if err := json.Unmarshal([]byte(started.followUps[0].Content), &snap); err != nil || snap.ID == "" {
					t.Fatalf("start snapshot: %v", err)
				}
				raw, _ := json.Marshal(map[string]string{"action": "wait", "id": snap.ID})
				return call(llm.ToolCall{ID: "wait", Name: "process_session", Arguments: string(raw)})
			}
			var first toolResult
			if managedFirst {
				first = managed()
			} else {
				first = call(verboseCheckCall("failure", false))
			}
			if !first.failed || !loop.failedChecks.unresolved() {
				t.Fatal("real check did not fail")
			}
			fixed := call(llm.ToolCall{ID: "fix", Name: "patch_file", Arguments: `{"path":"value.go","old":"return false","new":"return true"}`})
			if fixed.failed {
				t.Fatalf("patch: %+v", fixed)
			}
			var second toolResult
			if managedFirst {
				second = call(verboseCheckCall("success", false))
			} else {
				second = managed()
			}
			if second.failed {
				t.Fatalf("corrected test failed: %+v", second)
			}
			done := call(llm.ToolCall{ID: "done", Name: "goal", Arguments: `{"action":"complete_task"}`})
			if done.failed || *completed != 1 || loop.failedChecks.unresolved() {
				t.Fatalf("cross-path verification blocked: completed=%d unresolved=%v result=%+v", *completed, loop.failedChecks.unresolved(), done)
			}
		})
	}
}
