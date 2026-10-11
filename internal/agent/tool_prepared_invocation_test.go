package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/core"
)

const preparedInvocationSchema = `{"type":"object","properties":{"query":{"type":"string"},"count":{"type":"integer","minimum":1},"refresh":{"type":"boolean"}},"required":["query","count"],"additionalProperties":false}`

func TestToolPreparationRunsOnceOnMissAndRefresh(t *testing.T) {
	for _, replay := range []bool{false, true} {
		t.Run(map[bool]string{false: "observation", true: "verified-effect"}[replay], func(t *testing.T) {
			preparations, executions := 0, 0
			reg := tools.NewRegistry()
			tool := tools.Tool{Name: "inspect_resource", Description: "generic fixture", ReadOnly: true,
				ReuseTTL: time.Minute, RefreshArg: "refresh", Schema: preparedInvocationSchema,
				RepairArgs: func(raw json.RawMessage) (json.RawMessage, bool) {
					preparations++
					switch string(raw) {
					case `{"legacy":"a"}`:
						return json.RawMessage(`{"query":"a","count":"1"}`), true
					case `{"legacy":"b"}`:
						return json.RawMessage(`{"query":"b","count":"1"}`), true
					case `{"legacy":"a","refresh":true}`:
						return json.RawMessage(`{"query":"a","count":"1","refresh":"true"}`), true
					}
					return nil, false
				},
				Fn: func(_ context.Context, raw json.RawMessage) (tools.Result, error) {
					executions++
					if strings.Contains(string(raw), `"count":"`) || !strings.Contains(string(raw), `"count":1`) {
						t.Fatalf("execution did not receive accepted coerced arguments: %s", raw)
					}
					res := tools.Result{Text: "Verified resource " + string(raw)}
					if replay {
						res.Operation = &core.VerifiedOperation{Summary: "Existing observation verified"}
					}
					return res, nil
				}}
			if replay {
				tool.ReplaySuccess = func(_ context.Context, _ json.RawMessage, prior tools.Result) (tools.Result, bool) {
					prior.Inert = true
					return prior, true
				}
			}
			reg.MustRegister(tool)
			reg.MarkAlwaysOn(tool.Name)
			loop := makeLoop(t, echoProvider("fixture"), reg, "fixture")
			call := func(id, raw string) toolResult {
				return loop.invoke(context.Background(), llm.ToolCall{ID: id, Name: tool.Name, Arguments: raw}, make(chan Event, 16))
			}
			first := call("first", `{"legacy":"a"}`)
			if first.failed || preparations != 1 || executions != 1 {
				t.Fatalf("miss preparations/executions=%d/%d result=%+v", preparations, executions, first)
			}
			hit := call("hit", `{"legacy":"a"}`)
			if hit.failed || preparations != 2 || executions != 1 || len(hit.followUps) != 1 || hit.followUps[0].ToolCallID != "hit" {
				t.Fatalf("hit did not retain validation/protocol: %d/%d %+v", preparations, executions, hit)
			}
			freshArgs := `{"legacy":"a","refresh":true}`
			if replay {
				freshArgs = `{"legacy":"b"}` // Distinct requested output must execute.
			}
			fresh := call("fresh", freshArgs)
			if fresh.failed || preparations != 3 || executions != 2 {
				t.Fatalf("fresh preparations/executions=%d/%d result=%+v", preparations, executions, fresh)
			}
			invalid := call("invalid", `{"query":"a","count":0}`)
			if !invalid.failed || executions != 2 {
				t.Fatal("invalid arguments reused or executed")
			}
		})
	}
}

func TestPreparedReplayFallbackKeepsAcceptedArguments(t *testing.T) {
	reg := tools.NewRegistry()
	executions := 0
	reg.MustRegister(tools.Tool{Name: "export_resource", Description: "generic fixture", Schema: preparedInvocationSchema,
		Fn: func(_ context.Context, raw json.RawMessage) (tools.Result, error) {
			executions++
			var args struct {
				Query string
				Count int
			}
			if json.Unmarshal(raw, &args) != nil || args.Query != "a" || args.Count != 1 {
				t.Fatalf("replay callback mutated execution arguments: %s", raw)
			}
			return tools.Result{Text: "Export verified", Operation: &core.VerifiedOperation{Summary: "Export verified"}}, nil
		},
		ReplaySuccess: func(_ context.Context, raw json.RawMessage, _ tools.Result) (tools.Result, bool) {
			copy(raw, strings.Repeat("x", len(raw)))
			return tools.Result{}, false
		}})
	reg.MarkAlwaysOn("export_resource")
	loop := makeLoop(t, echoProvider("fixture"), reg, "fixture")
	for _, id := range []string{"initial", "missing-output"} {
		got := loop.invoke(context.Background(), llm.ToolCall{ID: id, Name: "export_resource", Arguments: `{"query":"a","count":"1"}`}, make(chan Event, 16))
		if got.failed {
			t.Fatalf("fallback failed: %+v", got)
		}
	}
	if executions != 2 {
		t.Fatalf("missing output executions=%d", executions)
	}
}

func TestPreparedReplayFallbackKeepsVerificationArguments(t *testing.T) {
	base := t.TempDir()
	reg := tools.NewRegistry()
	executions := 0
	reg.MustRegister(tools.Tool{Name: "file_write", Description: "verified replay fallback fixture",
		Schema: `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`,
		Fn: func(_ context.Context, raw json.RawMessage) (tools.Result, error) {
			var args struct{ Path string }
			if json.Unmarshal(raw, &args) != nil || args.Path != "safe.bin" {
				t.Fatalf("execution arguments changed: %s", raw)
			}
			executions++
			if err := os.WriteFile(filepath.Join(base, args.Path), []byte("verified fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			return tools.Result{Text: "File saved", Operation: &core.VerifiedOperation{Summary: "File saved"}}, nil
		},
		ReplaySuccess: func(_ context.Context, raw json.RawMessage, _ tools.Result) (tools.Result, bool) {
			copy(raw, `{"path":"evil.bin"}`)
			return tools.Result{}, false
		}})
	reg.MarkAlwaysOn("file_write")
	loop, err := NewLoop(LoopConfig{Provider: echoProvider("fixture"), Registry: reg, System: "fixture", BaseDir: base})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"initial", "fallback"} {
		got := loop.invoke(context.Background(), llm.ToolCall{ID: id, Name: "file_write", Arguments: `{"path":"safe.bin"}`}, make(chan Event, 16))
		if got.failed {
			t.Fatalf("verification did not use the executed path: %+v", got)
		}
	}
	if executions != 2 {
		t.Fatalf("executions=%d", executions)
	}
	if _, err := os.Stat(filepath.Join(base, "evil.bin")); !os.IsNotExist(err) {
		t.Fatal("replay changed the written destination")
	}
}

func TestPreparedReplayCannotDispatchAfterCancellation(t *testing.T) {
	reg := tools.NewRegistry()
	executions := 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg.MustRegister(tools.Tool{Name: "export_resource", Description: "generic fixture", Schema: `{}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			executions++ // Deliberately ignores cancellation.
			return tools.Result{Text: "Export verified", Operation: &core.VerifiedOperation{Summary: "Export verified"}}, nil
		},
		ReplaySuccess: func(context.Context, json.RawMessage, tools.Result) (tools.Result, bool) {
			cancel()
			return tools.Result{}, false
		}})
	reg.MarkAlwaysOn("export_resource")
	loop := makeLoop(t, echoProvider("fixture"), reg, "fixture")
	call := llm.ToolCall{ID: "initial", Name: "export_resource", Arguments: `{}`}
	if got := loop.invoke(ctx, call, make(chan Event, 16)); got.failed {
		t.Fatal(got)
	}
	call.ID = "cancelled-check"
	got := loop.invoke(ctx, call, make(chan Event, 16))
	if executions != 1 || !got.failed || len(got.followUps) != 1 || !strings.Contains(got.followUps[0].Content, "TOOL_NOT_STARTED") {
		t.Fatalf("cancelled replay fallback was dispatched: executions=%d result=%+v", executions, got)
	}
}

func TestPreparedReplayRejectsChangedRegistry(t *testing.T) {
	firstRegistry, replacement := tools.NewRegistry(), tools.NewRegistry()
	oldCalls, newCalls := 0, 0
	var loop *Loop
	firstRegistry.MustRegister(tools.Tool{Name: "export_resource", Description: "initial fixture", Schema: `{}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			oldCalls++
			return tools.Result{Text: "Export verified", Operation: &core.VerifiedOperation{Summary: "Export verified"}}, nil
		},
		ReplaySuccess: func(context.Context, json.RawMessage, tools.Result) (tools.Result, bool) {
			loop.registry = replacement
			return tools.Result{}, false
		}})
	replacement.MustRegister(tools.Tool{Name: "export_resource", Description: "replacement fixture", Schema: `{}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			newCalls++
			return tools.Result{Text: "unexpected dispatch"}, nil
		}})
	firstRegistry.MarkAlwaysOn("export_resource")
	replacement.MarkAlwaysOn("export_resource")
	loop = makeLoop(t, echoProvider("fixture"), firstRegistry, "fixture")
	for _, id := range []string{"initial", "changed-registry"} {
		got := loop.invoke(context.Background(), llm.ToolCall{ID: id, Name: "export_resource", Arguments: `{}`}, make(chan Event, 16))
		if id == "initial" && got.failed || id != "initial" && !got.failed {
			t.Fatalf("registry boundary result=%+v", got)
		}
	}
	if oldCalls != 1 || newCalls != 0 {
		t.Fatalf("stale token dispatched across registries: old/new=%d/%d", oldCalls, newCalls)
	}
}
