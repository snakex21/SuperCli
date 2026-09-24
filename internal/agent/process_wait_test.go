package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/processsession"
)

func TestProcessWaitHelper(t *testing.T) {
	if os.Getenv("SUPERCLI_WAIT_HELPER") != "1" {
		return
	}
	fmt.Println("completed process output")
	if os.Getenv("SUPERCLI_WAIT_HELPER_FAIL") == "1" {
		fmt.Fprintln(os.Stderr, "specific failed check")
		os.Exit(7)
	}
	os.Exit(0)
}

func TestProcessWaitOutcomeReachesNextModelRequest(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("thin=%v/fail=%v", thin, fail), func(t *testing.T) {
				tool := processsession.New(t.TempDir())
				defer tool.Close()
				reg := tools.NewRegistry()
				reg.MustRegister(tool.Spec())
				reg.Activate("process_session")
				command := []string{os.Args[0], "-test.run=^TestProcessWaitHelper$"}
				env := []string{"SUPERCLI_WAIT_HELPER=1"}
				if fail {
					env = append(env, "SUPERCLI_WAIT_HELPER_FAIL=1")
				}
				raw, _ := json.Marshal(map[string]any{"action": "start", "command": command, "env": env, "yield_ms": 0})
				scripts := [][]llm.Delta{
					{{ToolCall: &llm.ToolCall{ID: "start", Name: "process_session", Arguments: string(raw)}}},
					{{ToolCall: &llm.ToolCall{ID: "wait", Name: "process_session", Arguments: `{"action":"wait","id":"proc-1"}`}}},
					{{Content: "Finished with the recorded process outcome.", FinishReason: "stop"}},
				}
				if thin {
					cmdJSON, _ := json.Marshal(command)
					envJSON, _ := json.Marshal(env)
					scripts[0] = []llm.Delta{{Content: fmt.Sprintf("«process_session\naction: start\ncommand: %s\nenv: %s\nyield_ms: 0»", cmdJSON, envJSON), FinishReason: "stop"}}
					scripts[1] = []llm.Delta{{Content: "«process_session\naction: wait\nid: proc-1»", FinishReason: "stop"}}
				}
				p := &stubProvider{name: "wait-test", scripts: scripts}
				l, err := NewLoop(LoopConfig{Provider: p, Registry: reg, ThinTools: thin, MaxSteps: 4})
				if err != nil {
					t.Fatal(err)
				}
				events := drainEvents(t, mustRun(t, l, "Run the command and wait for its result."))
				calls := 0
				for _, event := range events {
					if e, ok := event.(ErrorEvent); ok {
						t.Fatal(e.Err)
					}
					if _, ok := event.(ToolCallEvent); ok {
						calls++
					}
				}
				if calls != 2 || p.calls != 3 {
					t.Fatalf("tool calls=%d model requests=%d; want start + wait + final", calls, p.calls)
				}
				var result string
				for _, m := range p.reqs[2] {
					if m.Role == llm.RoleTool && m.Name == "process_session" {
						result = m.Content
					}
				}
				if fail {
					if !strings.Contains(result, "command_failed exit=7") {
						t.Fatalf("failure did not reach model: %s", result)
					}
				} else if !strings.Contains(result, `"status":"done"`) || !strings.Contains(result, `"exit_code":0`) {
					t.Fatalf("final status did not reach model: %s", result)
				}
			})
		}
	}
}
