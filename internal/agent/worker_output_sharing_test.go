package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

type unavailableWorkerOutputStore struct{ saves, reads int }

func (s *unavailableWorkerOutputStore) SaveToolOutput(context.Context, string, string) error {
	s.saves++
	return fmt.Errorf("fixture storage unavailable")
}
func (s *unavailableWorkerOutputStore) ReadToolOutput(context.Context, string) (string, error) {
	s.reads++
	return "", fmt.Errorf("fixture storage unavailable")
}

func TestWorkerOutputReferenceSurvivesWithoutPersistence(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, failedStore := range []bool{false, true} {
			for _, separateBase := range []bool{false, true} {
				t.Run(fmt.Sprintf("thin=%t/failed-store=%t/separate-base=%t", thin, failedStore, separateBase), func(t *testing.T) {
					ctx := context.Background()
					full := strings.Repeat("worker evidence\n", 1000) + "ORIGINAL_DETAIL\n" + strings.Repeat("last evidence\n", 1000)
					base := tools.NewRegistry()
					executions := 0
					base.MustRegister(tools.Tool{Name: "fixture_log", Description: "fixture", Schema: "{}", ReadOnly: true, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
						executions++
						return tools.Result{Text: full}, nil
					}})
					base.MarkAlwaysOn("fixture_log")
					provider := &outputReplayProvider{stubProvider: &stubProvider{name: "worker-shared-output", scripts: [][]llm.Delta{
						{{ToolCall: &llm.ToolCall{ID: "inspect", Name: "fixture_log", Arguments: "{}"}, FinishReason: "tool_calls"}},
						{{Content: "Completed. Saved handle=out_000001", FinishReason: "stop"}},
					}}}
					parentReg := base
					if separateBase {
						parentReg = tools.NewRegistry()
					}
					var persistence tools.OutputPersistence
					unavailable := &unavailableWorkerOutputStore{}
					if failedStore {
						persistence = unavailable
					}
					parent, err := NewLoop(LoopConfig{Provider: provider, Registry: parentReg, ThinTools: thin, ToolOutputs: persistence})
					if err != nil {
						t.Fatal(err)
					}
					specs := NewSubAgentRegistry()
					MustRegisterAll(specs, BuiltinSubAgents())
					task, err := NewAgentTool(specs, parent, base, provider, nil, NewLoop)
					if err != nil {
						t.Fatal(err)
					}
					result, err := task.execute(ctx, json.RawMessage(`{"prompt":"Inspect evidence."}`))
					if err != nil || result.Err != nil {
						t.Fatalf("worker failed: %v %v", err, result.Err)
					}
					handle := handleInOutput(result.Text)
					if handle == "" {
						t.Fatal("worker omitted reference")
					}
					args := map[string]any{"handle": handle, "query": "ORIGINAL_DETAIL"}
					raw, _ := json.Marshal(args)
					readCall := llm.ToolCall{ID: "read-saved", Name: "read_output", Arguments: string(raw)}
					if thin {
						ensureWorkerDiscovery(parentReg)
						wrapped, _ := json.Marshal(map[string]any{"tool": "read_output", "args": args})
						readCall.Name = "invoke_tool"
						readCall.Arguments = string(wrapped)
					}
					read := parent.invoke(ctx, readCall, make(chan Event, 8))
					if read.failed || len(read.followUps) != 1 || !strings.Contains(read.followUps[0].Content, "ORIGINAL_DETAIL") {
						t.Fatalf("parent lost worker evidence: %+v", read)
					}
					if executions != 1 || len(provider.reqs) != 2 {
						t.Fatalf("repeated operation: executions=%d requests=%d", executions, len(provider.reqs))
					}
					if unavailable.reads != 0 {
						t.Fatalf("read persistence despite live evidence: %d", unavailable.reads)
					}
					child, ok := task.Workers.Get("worker-1")
					if !ok || child.Loop.writer != nil || child.Loop.registry == parentReg {
						t.Fatal("worker isolation changed")
					}
					t.Logf("one execution; %d model requests; %d persistence reads", len(provider.reqs), unavailable.reads)
				})
			}
		}
	}
}
