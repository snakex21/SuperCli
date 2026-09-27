package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/ctxexec"
)

func TestCompleteCommandFailureEvidenceArrivesWithoutAnotherTool(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprint(thin), func(t *testing.T) {
			root := t.TempDir()
			reg := tools.NewRegistry()
			tool := tools.NewCtxExecuteTool(ctxexec.New(root), root).Spec()
			execute := tool.Fn
			executions := 0
			tool.Fn = func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
				executions++
				return execute(ctx, args)
			}
			reg.MustRegister(tool)
			reg.MarkAlwaysOn("ctx_execute")
			command := []string{os.Args[0], "-test.run=^TestCommandCaptureHelper$"}
			env := []string{"SUPERCLI_COMMAND_CAPTURE_HELPER=medium_failure"}
			raw, _ := json.Marshal(map[string]any{"command": command, "env_extra": env, "max_stdout_kb": 64, "max_stderr_kb": 64})
			scripts := [][]llm.Delta{
				{{ToolCall: &llm.ToolCall{ID: "command", Name: "ctx_execute", Arguments: string(raw)}}},
				{{Content: "The observed command failed.", FinishReason: "stop"}},
			}
			if thin {
				cmdJSON, _ := json.Marshal(command)
				envJSON, _ := json.Marshal(env)
				scripts[0] = []llm.Delta{{Content: fmt.Sprintf("«ctx_execute\ncommand: %s\nenv_extra: %s\nmax_stdout_kb: 64\nmax_stderr_kb: 64»", cmdJSON, envJSON), FinishReason: "stop"}}
			}
			provider := &outputReplayProvider{stubProvider: &stubProvider{name: "complete-capture", scripts: scripts}}
			loop, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, ThinTools: thin, MaxSteps: 3})
			if err != nil {
				t.Fatal(err)
			}
			events := drainEvents(t, mustRun(t, loop, "Run this diagnostic command and explain its output."))
			if executions != 1 || provider.calls != 2 {
				t.Fatalf("commands=%d requests=%d", executions, provider.calls)
			}
			sawUI := false
			for _, event := range events {
				if failure, ok := event.(ErrorEvent); ok {
					t.Fatal(failure.Err)
				}
				if result, ok := event.(ToolResultEvent); ok {
					sawUI = result.Err != nil && strings.Contains(result.Output, "source.go:731:19: undefined: missingSymbol")
				}
			}
			if !sawUI {
				t.Fatal("UI lost full failure")
			}
			found := false
			for _, m := range provider.reqs[1] {
				if m.Role == llm.RoleTool {
					found = true
					for _, want := range []string{"command_failed exit=7", "source.go:731:19: undefined: missingSymbol", "final build status", "handle=out_"} {
						if !strings.Contains(m.Content, want) {
							t.Errorf("lost %q in failed result", want)
						}
					}
					if len(m.Content) > 6000 {
						t.Fatalf("excessive preview: %d", len(m.Content))
					}
				}
			}
			if !found {
				t.Fatal("missing tool result in next request")
			}
		})
	}
}
