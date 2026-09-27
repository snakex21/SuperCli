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

func TestResumedSessionOwnsNewToolOutputs(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, kind := range []string{"tool", "new worker", "pruned inline"} {
			for _, deleteCurrent := range []bool{false, true} {
				t.Run(fmt.Sprintf("thin=%t/%s/delete-current=%t", thin, kind, deleteCurrent), func(t *testing.T) {
					ctx := context.Background()
					dir := t.TempDir()
					store, err := session.OpenStore(dir)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = store.Close() })
					previous, err := store.Create("fixture", "fixture", "previous")
					if err != nil {
						t.Fatal(err)
					}
					current, err := store.Create("fixture", "fixture", "current")
					if err != nil {
						t.Fatal(err)
					}
					original := strings.Repeat("source evidence before\n", 700) + "SAVED_DETAIL=6842\n" + strings.Repeat("source evidence after\n", 700)
					if kind == "pruned inline" {
						original = strings.Repeat("source evidence\n", 100) + "SAVED_DETAIL=6842\n"
					}
					reg := tools.NewRegistry()
					executions := 0
					reg.MustRegister(tools.Tool{Name: "fixture_evidence", Description: "fixture evidence", Schema: "{}", ReadOnly: true, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
						executions++
						return tools.Result{Text: original}, nil
					}})
					reg.MarkAlwaysOn("fixture_evidence")
					if thin {
						ensureWorkerDiscovery(reg)
					}
					first := []llm.Delta{{ToolCall: &llm.ToolCall{ID: "collect", Name: "fixture_evidence", Arguments: "{}"}, FinishReason: "tool_calls"}}
					if thin {
						first = []llm.Delta{{Content: "«fixture_evidence»", FinishReason: "stop"}}
					}
					provider := &stubProvider{name: "resume-owner", scripts: [][]llm.Delta{first, {{Content: "Evidence collected.", FinishReason: "stop"}}}}
					parent, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, Writer: session.NewWriter(store, previous.ID), ThinTools: thin})
					if err != nil {
						t.Fatal(err)
					}
					history := []llm.Message{{Role: llm.RoleUser, Content: "Continue the saved conversation."}}
					if err := parent.ResumeConversation(ctx, session.NewWriter(store, current.ID), history, nil); err != nil {
						t.Fatal(err)
					}
					if parent.SessionID() != current.ID {
						t.Fatal("conversation did not switch")
					}
					handle := ""
					if kind == "new worker" {
						specs := NewSubAgentRegistry()
						MustRegisterAll(specs, BuiltinSubAgents())
						task, err := NewAgentTool(specs, parent, reg, provider, nil, NewLoop)
						if err != nil {
							t.Fatal(err)
						}
						result, err := task.execute(ctx, json.RawMessage("{\"prompt\":\"Inspect the evidence.\"}"))
						if err != nil || result.Err != nil {
							t.Fatalf("worker: %v %v", err, result.Err)
						}
						worker, ok := task.Workers.Get("worker-1")
						if !ok {
							t.Fatal("missing worker")
						}
						for _, message := range worker.Loop.Messages {
							if message.Role == llm.RoleTool {
								if h := handleInOutput(message.Content); h != "" {
									handle = h
									break
								}
							}
						}
						if len(provider.reqs) != 2 {
							t.Fatalf("worker requests=%d", len(provider.reqs))
						}
					} else {
						call := llm.ToolCall{ID: "collect", Name: "fixture_evidence", Arguments: "{}"}
						if thin {
							call.Name = "invoke_tool"
							call.Arguments = "{\"tool\":\"fixture_evidence\",\"args\":{}}"
						}
						result := parent.invoke(ctx, call, make(chan Event, 8))
						if result.failed || len(result.followUps) != 1 {
							t.Fatalf("result: %+v", result)
						}
						handle = handleInOutput(result.followUps[0].Content)
						if kind == "pruned inline" {
							if handle != "" {
								t.Fatal("fixture was not inline")
							}
							parent.Messages = append(parent.Messages, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}}, result.followUps[0], llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "current", Name: "fixture_evidence", Arguments: "{}"}}}, llm.Message{Role: llm.RoleTool, Name: "fixture_evidence", ToolCallID: "current", Content: strings.Repeat("protected ", 40)})
							parent.windowFor = func(string) int { return 1000 }
							parent.pruneProtect = 1
							if parent.maybePruneToolResults(ctx, nil) == 0 {
								t.Fatal("not pruned")
							}
							_, raw, ok := strings.Cut(parent.Messages[2].Content, "read_output ")
							if !ok {
								t.Fatal("no archive reference")
							}
							var ref struct{ Handle string }
							if err := json.Unmarshal([]byte(strings.TrimSuffix(raw, "]")), &ref); err != nil {
								t.Fatal(err)
							}
							handle = ref.Handle
						}
						if len(provider.reqs) != 0 {
							t.Fatal("tool recovery called the provider")
						}
					}
					if handle == "" {
						t.Fatal("missing retained-output reference")
					}
					// Remove a conversation and reopen the actual database. A warm shared cache
					// otherwise masks outputs being owned by the wrong session.
					deleted := previous.ID
					if deleteCurrent {
						deleted = current.ID
					}
					if err := store.Delete(deleted); err != nil {
						t.Fatal(err)
					}
					if err := store.Close(); err != nil {
						t.Fatal(err)
					}
					store, err = session.OpenStore(dir)
					if err != nil {
						t.Fatal(err)
					}
					fresh := tools.NewRegistry()
					if thin {
						ensureWorkerDiscovery(fresh)
					}
					reader, err := NewLoop(LoopConfig{Provider: echoProvider("cold-reader"), Registry: fresh, Writer: session.NewWriter(store, current.ID), ThinTools: thin})
					if err != nil {
						t.Fatal(err)
					}
					raw, _ := json.Marshal(map[string]any{"handle": handle, "query": "SAVED_DETAIL=6842"})
					call := llm.ToolCall{ID: "recover", Name: "read_output", Arguments: string(raw)}
					if thin {
						call.Name = "invoke_tool"
						call.Arguments = "{\"tool\":\"read_output\",\"args\":" + string(raw) + "}"
					}
					recovered := reader.invoke(ctx, call, make(chan Event, 8))
					found := !recovered.failed && len(recovered.followUps) == 1 && strings.Contains(recovered.followUps[0].Content, "SAVED_DETAIL=6842")
					if found == deleteCurrent {
						t.Fatalf("wrong output owner: delete-current=%t found=%t", deleteCurrent, found)
					}
					if executions != 1 {
						t.Fatalf("source executed %d times", executions)
					}
				})
			}
		}
	}
}
