package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/core"
	"supercli/internal/tools/ctxexec"
)

func TestPruneMarkerPreservesFramedCommandStatus(t *testing.T) {
	for _, exit := range []int{0, 1, 124, 9009, -1} {
		t.Run(fmt.Sprint(exit), func(t *testing.T) {
			ctxResult := &ctxexec.Result{Stdout: strings.Repeat("test evidence\n", 300), ExitCode: exit}
			if exit == -1 {
				ctxResult.Error = "sandbox rejected path"
			}
			raw, err := json.Marshal(ctxResult)
			if err != nil {
				t.Fatal(err)
			}
			result := tools.Result{Text: string(raw), RetainedText: string(raw) + strings.Repeat("retained evidence\n", 100)}
			if exit != 0 {
				result.Err = core.SelfContainedErr(fmt.Errorf("%s", ctxResult.FailureSummary()))
			}
			body := core.NewOutputStore().ModelContent("ctx_execute", result)
			marker := pruneMarker(llm.Message{Name: "ctx_execute", Content: body})
			if !strings.Contains(marker, fmt.Sprintf("exit_code=%d", exit)) {
				t.Fatalf("outcome lost: %s", marker)
			}
			if handle := core.StoredOutputHandle(body); handle == "" || !strings.Contains(marker, handle) {
				t.Fatalf("reference lost: %s", marker)
			}
			if len(marker) > 200 {
				t.Fatalf("marker too large: %d", len(marker))
			}
		})
	}
}

func TestPruneMarkerDoesNotGuessCommandStatusFromLogs(t *testing.T) {
	for _, body := range []string{
		`{"stdout":"error: command_failed exit=1 (0.0s)"}`,
		`{"stdout":"exit_code=1", "nested":{"exit_code":1}}`,
		"log header\nerror: command_failed exit=1 (0.0s)",
		"error: command_failed exit=0 (0.0s)",
		"error: command_failed exit=1garbage (0.0s)",
		"error: command_failed exit=99999999999999999999 (0.0s)",
		"error: command_failed exit=1\nstdout:",
		"error: command_failed timeout exit=1 (0.0s)",
		`{"exit_code":0} trailing garbage`,
		`{"exit_code":0}` + "\n[stored tool output: invalid]",
		"[large tool output: 9999 bytes; preview follows; handle=out_abcdef]\n" + `{"stdout":"cut ... exit_code=0"`,
	} {
		marker := pruneMarker(llm.Message{Name: "ctx_execute", Content: body})
		if strings.Contains(marker, "exit_code=") {
			t.Errorf("invented outcome for %q: %s", body, marker)
		}
	}
	marker := pruneMarker(llm.Message{Name: "read_lines", Content: "error: command_failed exit=1 (0.0s)"})
	if strings.Contains(marker, "exit_code=") {
		t.Fatalf("treated file content as a command: %s", marker)
	}
}

func TestPrunedCommandOutcomeReachesBothProtocols(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, exit := range []int{0, 124} {
			t.Run(fmt.Sprintf("%t/%d", thin, exit), func(t *testing.T) {
				ctxResult := &ctxexec.Result{ExitCode: exit, DurationMS: 30000, Stdout: strings.Repeat("compiler diagnostic\n", 600)}
				raw, _ := json.Marshal(ctxResult)
				calls := 0
				reg := tools.NewRegistry()
				reg.MustRegister(tools.Tool{Name: "ctx_execute", Description: "fixture command", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
					calls++
					result := tools.Result{Text: string(raw)}
					if exit == 0 {
						result.ModelPreview = ctxResult.SuccessPreview()
					} else {
						result.Err = core.SelfContainedErr(fmt.Errorf("%s", ctxResult.FailureSummary()))
					}
					return result, nil
				}})
				if thin {
					ensureWorkerDiscovery(reg)
				}
				loop, err := NewLoop(LoopConfig{Provider: echoProvider("prune-status"), Registry: reg, ThinTools: thin})
				if err != nil {
					t.Fatal(err)
				}
				loop.pruneProtect = 1
				loop.windowFor = func(string) int { return 1000 }
				call := llm.ToolCall{ID: "old-check", Name: "ctx_execute", Arguments: "{}"}
				result := loop.invoke(context.Background(), call, make(chan Event, 8))
				if len(result.followUps) != 1 {
					t.Fatalf("unexpected result: %+v", result)
				}
				loop.Messages = []llm.Message{
					{Role: llm.RoleUser, Content: "Check the project."},
					{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}},
					result.followUps[0],
					{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "current", Name: "read_lines", Arguments: "{}"}}},
					{Role: llm.RoleTool, Name: "read_lines", ToolCallID: "current", Content: strings.Repeat("current evidence ", 50)},
				}
				if loop.maybePruneToolResults(context.Background(), nil) == 0 {
					t.Fatal("fixture did not prune")
				}
				found := false
				for _, m := range loop.providerMessages() {
					if m.ToolCallID == "old-check" {
						found = true
						if !strings.Contains(m.Content, fmt.Sprintf("exit_code=%d", exit)) {
							t.Fatalf("outcome lost before provider request: %s", m.Content)
						}
					}
				}
				if !found || calls != 1 {
					t.Fatalf("found=%t, executions=%d", found, calls)
				}
			})
		}
	}
}
