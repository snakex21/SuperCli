package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

func TestPrunedOutputRemainsRetrievableWithoutRepeatingOperation(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, durable := range []bool{false, true} {
			for _, kind := range []string{"large", "failure", "structured"} {
				t.Run(fmt.Sprintf("thin=%t/durable=%t/%s", thin, durable, kind), func(t *testing.T) {
					ctx := context.Background()
					var persistence tools.OutputPersistence
					if durable {
						store, err := session.OpenStore(t.TempDir())
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { _ = store.Close() })
						sess, err := store.Create("prune", "fixture", "")
						if err != nil {
							t.Fatal(err)
						}
						persistence = session.NewWriter(store, sess.ID)
					}
					full := strings.Repeat("old output line\n", 700) + "RECOVER_THIS_DETAIL\n" + strings.Repeat("remaining output\n", 700)
					calls := 0
					reg := tools.NewRegistry()
					reg.MustRegister(tools.Tool{Name: "fixture_log", Description: "fixture", Schema: "{}", ReadOnly: true,
						Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
							calls++
							r := tools.Result{Text: full}
							switch kind {
							case "failure":
								r.Err = fmt.Errorf("check failed")
							case "structured":
								r.ModelPreview = strings.Repeat("bounded report summary\n", 150)
							}
							return r, nil
						},
					})
					if thin {
						ensureWorkerDiscovery(reg)
					}
					loop, err := NewLoop(LoopConfig{Provider: echoProvider("prune"), Registry: reg, ToolOutputs: persistence, ThinTools: thin})
					if err != nil {
						t.Fatal(err)
					}
					loop.pruneProtect = 1
					loop.windowFor = func(string) int { return 2000 }
					call := llm.ToolCall{ID: "old-read", Name: "fixture_log", Arguments: "{}"}
					result := loop.invoke(ctx, call, make(chan Event, 8))
					if len(result.followUps) != 1 {
						t.Fatalf("result: %+v", result)
					}
					originalHandle := handleInOutput(result.followUps[0].Content)
					if originalHandle == "" {
						t.Fatal("fixture did not retain the full output")
					}
					loop.Messages = []llm.Message{
						{Role: llm.RoleUser, Content: "Inspect the log."},
						{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}},
						result.followUps[0],
						{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "current", Name: "read_lines", Arguments: "{}"}}},
						{Role: llm.RoleTool, Name: "read_lines", ToolCallID: "current", Content: strings.Repeat("active evidence ", 200)},
					}
					before := loop.EstimateVisibleTokens()
					reclaimed := loop.maybePruneToolResults(ctx, nil)
					if reclaimed == 0 {
						t.Fatal("fixture did not prune")
					}
					marker := loop.Messages[2].Content
					if !strings.HasPrefix(marker, pruneMarkerPrefix) || !strings.Contains(marker, originalHandle) {
						t.Fatalf("prune lost the existing output handle: %q", marker)
					}
					_, raw, ok := strings.Cut(marker, "read_output ")
					if !ok {
						t.Fatalf("no retrieval call in marker: %q", marker)
					}
					var args map[string]any
					if err := json.Unmarshal([]byte(strings.TrimSuffix(raw, "]")), &args); err != nil {
						t.Fatal(err)
					}
					if args["handle"] != originalHandle {
						t.Fatal("reference was replaced")
					}
					if len(marker) > 200 || before-loop.EstimateVisibleTokens() != reclaimed {
						t.Fatal("prune budget no longer accounts for the reference")
					}
					// The same immutable reference also survives a fresh loop when persisted.
					reader := loop
					if durable {
						fresh := tools.NewRegistry()
						if thin {
							ensureWorkerDiscovery(fresh)
						}
						reader, err = NewLoop(LoopConfig{Provider: echoProvider("resume"), Registry: fresh, ToolOutputs: persistence, ThinTools: thin})
						if err != nil {
							t.Fatal(err)
						}
					}
					args["query"] = "RECOVER_THIS_DETAIL"
					rawArgs, _ := json.Marshal(args)
					readCall := llm.ToolCall{ID: "recover", Name: "read_output", Arguments: string(rawArgs)}
					if thin {
						wrapped, _ := json.Marshal(map[string]any{"tool": "read_output", "args": args})
						readCall.Name, readCall.Arguments = "invoke_tool", string(wrapped)
					}
					recovered := reader.invoke(ctx, readCall, make(chan Event, 8))
					if recovered.failed || len(recovered.followUps) != 1 || !strings.Contains(recovered.followUps[0].Content, "RECOVER_THIS_DETAIL") {
						t.Fatalf("saved detail unavailable: %+v", recovered)
					}
					if calls != 1 {
						t.Fatalf("original operation repeated %d times", calls)
					}
					loop.maybePruneToolResults(ctx, nil)
					if loop.Messages[2].Content != marker {
						t.Fatal("pruned prefix changed again")
					}
					t.Logf("provider result: %d -> %d bytes, one original execution", len(result.followUps[0].Content), len(marker))
				})
			}
		}
	}
}
