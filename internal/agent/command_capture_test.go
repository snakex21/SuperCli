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

func TestCommandCaptureHelper(t *testing.T) {
	mode := os.Getenv("SUPERCLI_COMMAND_CAPTURE_HELPER")
	for _, arg := range os.Args {
		if arg == "--capture-fixture" {
			mode = "failure"
		}
	}
	if mode == "" {
		return
	}
	for _, stream := range []*os.File{os.Stdout, os.Stderr} {
		fmt.Fprint(stream, "source.go:731:19: undefined: missingSymbol\n", strings.Repeat("later build output\n", 10000), "final build status\n")
	}
	if mode == "failure" {
		os.Exit(7)
	}
	os.Exit(0)
}

// Use the real command runner: retaining a fake tool's full text alone missed
// the runner discarding everything before the last 16/4 KiB of output.
func TestCommandCaptureRetrievalAcrossModelRoutes(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, mode := range []string{"success", "failure"} {
			t.Run(fmt.Sprintf("thin=%v/%s", thin, mode), func(t *testing.T) {
				home := t.TempDir()
				reg := tools.NewRegistry()
				tool := tools.NewCtxExecuteTool(ctxexec.New(home), home).Spec()
				execute := tool.Fn
				executions := 0
				tool.Fn = func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
					executions++
					return execute(ctx, args)
				}
				reg.MustRegister(tool)
				reg.MarkAlwaysOn("ctx_execute")
				command := []string{os.Args[0], "-test.run=^TestCommandCaptureHelper$"}
				env := []string{"SUPERCLI_COMMAND_CAPTURE_HELPER=" + mode}
				raw, _ := json.Marshal(map[string]any{"command": command, "env_extra": env})
				scripts := [][]llm.Delta{
					{{ToolCall: &llm.ToolCall{ID: "command", Name: "ctx_execute", Arguments: string(raw)}}},
					{{ToolCall: &llm.ToolCall{ID: "inspect", Name: "read_output", Arguments: `{"handle":"out_000001","query":"missingSymbol"}`}}},
					{{Content: "Inspected the original command evidence.", FinishReason: "stop"}},
				}
				if thin {
					cmdJSON, _ := json.Marshal(command)
					envJSON, _ := json.Marshal(env)
					scripts[0] = []llm.Delta{{Content: fmt.Sprintf("«ctx_execute\ncommand: %s\nenv_extra: %s»", cmdJSON, envJSON), FinishReason: "stop"}}
					scripts[1] = []llm.Delta{{Content: "«read_output\nhandle: out_000001\nquery: missingSymbol»", FinishReason: "stop"}}
				}
				provider := &outputReplayProvider{stubProvider: &stubProvider{name: "command-capture", scripts: scripts}}
				loop, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, ThinTools: thin, MaxSteps: 4})
				if err != nil {
					t.Fatal(err)
				}
				events := drainEvents(t, mustRun(t, loop, "Run the command and inspect its early diagnostic."))
				for _, event := range events {
					if failure, ok := event.(ErrorEvent); ok {
						t.Fatal(failure.Err)
					}
				}
				if executions != 1 || provider.calls != 3 {
					t.Fatalf("executions=%d model calls=%d", executions, provider.calls)
				}
				var preview, found string
				for _, message := range provider.reqs[2] {
					if message.Role != llm.RoleTool {
						continue
					}
					switch message.Name {
					case "ctx_execute":
						preview = message.Content
					case "read_output":
						found = message.Content
					}
				}
				if !strings.Contains(preview, "handle=out_") || (mode == "success" && strings.Contains(preview, "missingSymbol")) || len(preview) > 6000 {
					t.Fatalf("bad bounded preview (%d bytes): %.300s", len(preview), preview)
				}
				if mode == "failure" && (!strings.Contains(preview, "command_failed exit=7") || !strings.Contains(preview, "source.go:731:19: undefined: missingSymbol")) {
					t.Fatal("failure status lost")
				}
				if mode == "success" && !strings.Contains(preview, `"exit_code":0`) {
					t.Fatal("success metadata lost")
				}
				if !strings.Contains(found, "source.go:731:19: undefined: missingSymbol") || len(found) > 1500 {
					t.Fatalf("cannot inspect early evidence: %s", found)
				}
			})
		}
	}
}
