package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/system/execution"
	"supercli/internal/tools"
)

func TestTaskUsesSelectedBackendToolProfile(t *testing.T) {
	full := execution.Profile{StableToolset: true}
	thin := execution.Profile{ThinTools: true, StableToolset: true, CatalogHoist: true}
	dynamic := execution.Profile{ThinTools: true}
	for _, tc := range []struct {
		name   string
		parent execution.Profile
		worker *execution.Profile
		failed bool
		want   execution.Profile
	}{
		{"cloud to local", full, &thin, false, thin},
		{"local to cloud", thin, &full, false, full},
		{"explicit dynamic tools", full, &dynamic, false, dynamic},
		{"legacy caller inherits", thin, nil, false, thin},
		{"failed local falls back to cloud", full, &thin, true, full},
		{"failed cloud falls back to local", thin, &full, true, thin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var providers []llm.Provider
			at := newModelTestTool(t, &providers)
			parent := makeLoop(t, at.Provider, at.BaseRegistry, "")
			parent.thinTools, parent.stableToolset, parent.catalogHoist = tc.parent.ThinTools, tc.parent.StableToolset, tc.parent.CatalogHoist
			parent.baseDir, parent.contextProvider = t.TempDir(), "main-connection"
			at.ParentLoop = parent
			at.WorkerProvider = &stubReplyProvider{name: "worker-model", reply: "done"}
			at.WorkerContextProvider = "worker-connection"
			at.WorkerProfile = tc.worker
			if tc.failed {
				at.WorkerPing = func(context.Context) error { return errors.New("offline") }
			}
			result, err := at.execute(context.Background(), json.RawMessage(`{"prompt":"do it"}`))
			if err != nil || result.Err != nil {
				t.Fatalf("task: %v %+v", err, result)
			}
			worker, ok := at.Workers.Get("worker-1")
			if !ok {
				t.Fatal("missing worker")
			}
			l := worker.Loop
			if l.thinTools != tc.want.ThinTools || l.stableToolset != tc.want.StableToolset || l.catalogHoist != tc.want.CatalogHoist {
				t.Fatalf("selected loop flags=(%v,%v,%v), want %+v", l.thinTools, l.stableToolset, l.catalogHoist, tc.want)
			}
			wantProvider, wantScope := at.WorkerProvider, "worker-connection"
			if tc.failed {
				wantProvider, wantScope = at.Provider, "main-connection"
			}
			if len(providers) != 1 || providers[0] != wantProvider || l.contextProvider != wantScope {
				t.Fatalf("provider/scope mismatch: %v %q", providers, l.contextProvider)
			}
			if l.baseDir != parent.baseDir {
				t.Fatal("worker lost its workspace")
			}
		})
	}
}

func TestThinCodeWorkerCanDiscoverAndDispatchItsOwnTools(t *testing.T) {
	base := tools.NewRegistry()
	executions := 0
	base.MustRegister(tools.Tool{Name: "scratchpad", Description: "save structured notes", Schema: `{"type":"object","properties":{"notes":{"type":"array","items":{"type":"string"}}},"required":["notes"]}`,
		Fn: func(_ context.Context, args json.RawMessage) (tools.Result, error) {
			executions++
			if string(args) != `{"notes":["finding"]}` {
				t.Errorf("arguments=%s", args)
			}
			return tools.Result{Text: "notes saved"}, nil
		},
	})
	for _, name := range []string{"task", "private_tool"} {
		base.MustRegister(tools.Tool{Name: name, Description: "forbidden", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			t.Error("escaped restricted registry")
			return tools.Result{}, nil
		}})
	}
	p := &stubProvider{name: "worker", scripts: [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "discover", Name: "tool_search", Arguments: `{"query":"scratchpad"}`}}},
		{{ToolCall: &llm.ToolCall{ID: "save", Name: "invoke_tool", Arguments: `{"tool":"scratchpad","args":{"notes":["finding"]}}`}}},
		{{Content: "done", FinishReason: "stop"}},
	}}
	parent := makeLoop(t, p, base, "")
	parent.thinTools, parent.stableToolset, parent.catalogHoist = true, true, true
	specs := NewSubAgentRegistry()
	MustRegisterAll(specs, BuiltinSubAgents())
	task, err := NewAgentTool(specs, parent, base, p, nil, NewLoop)
	if err != nil {
		t.Fatal(err)
	}
	result, err := task.execute(context.Background(), json.RawMessage(`{"agent":"code","prompt":"Save the finding."}`))
	if err != nil || result.Err != nil {
		t.Fatalf("task: %v %+v", err, result)
	}
	if executions != 1 || p.calls != 3 {
		t.Fatalf("executions=%d requests=%d", executions, p.calls)
	}
	worker, ok := task.Workers.Get("worker-1")
	if !ok {
		t.Fatal("missing worker")
	}
	reg := worker.Loop.registry
	if !reg.IsActive("scratchpad") || base.IsActive("scratchpad") {
		t.Fatal("discovery must activate only the child's tool")
	}
	for _, name := range []string{"tool_search", "invoke_tool"} {
		if !reg.IsVisible(name) {
			t.Fatalf("missing protocol tool %s", name)
		}
	}
	for _, name := range []string{"task", "private_tool"} {
		if _, ok := reg.Get(name); ok {
			t.Fatalf("forbidden tool %s copied", name)
		}
		if _, err := resolveInvokeToolCall(reg, llm.ToolCall{Name: "invoke_tool", Arguments: fmt.Sprintf(`{"tool":%q,"args":{}}`, name)}); err == nil {
			t.Fatalf("dispatch allowed %s", name)
		}
	}
	for _, count := range p.toolReqs {
		if count != p.toolReqs[0] {
			t.Fatalf("stable tool schema changed: %v", p.toolReqs)
		}
	}
	found := false
	for _, m := range p.reqs[2] {
		if m.Role == llm.RoleTool && strings.Contains(m.Content, "notes saved") {
			found = true
		}
	}
	if !found {
		t.Fatal("execution evidence did not reach worker")
	}
}

func TestGeneralWorkerKeepsOptionalSchemasLazy(t *testing.T) {
	for _, discovery := range []bool{false, true} {
		t.Run(fmt.Sprintf("discovery=%v", discovery), func(t *testing.T) {
			base := tools.NewRegistry()
			for _, name := range []string{"read_lines", "list_dir", "already_selected", "rare_alpha", "rare_beta"} {
				base.MustRegister(tools.Tool{Name: name, Description: name, Schema: `{"type":"object","properties":{"value":{"type":"string"}}}`, ReadOnly: true,
					Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{Text: "ok"}, nil },
				})
			}
			base.Activate("already_selected")
			if discovery {
				base.MustRegister(tools.NewToolSearcher(base, nil).Spec())
			}
			child := restrictedRegistry(base, nil)
			loop := makeLoop(t, &stubProvider{}, child, "")
			defs := loop.buildToolDefs()
			names := make(map[string]bool)
			for _, def := range defs {
				names[def.Name] = true
			}
			for _, name := range []string{"read_lines", "list_dir", "already_selected"} {
				if !names[name] {
					t.Fatalf("common/selected tool %s requires redundant discovery", name)
				}
			}
			for _, name := range []string{"rare_alpha", "rare_beta"} {
				if _, ok := child.Get(name); !ok {
					t.Fatalf("lost optional tool %s", name)
				}
				if names[name] == discovery {
					t.Fatalf("optional schema visibility %s=%v", name, names[name])
				}
			}
			if !discovery {
				return
			}
			search, ok := child.Get("tool_search")
			if !ok {
				t.Fatal("missing discovery")
			}
			result, err := search.Fn(context.Background(), json.RawMessage(`{"query":"rare_alpha"}`))
			if err != nil || result.Err != nil {
				t.Fatalf("search: %v %+v", err, result)
			}
			if !child.IsActive("rare_alpha") || child.IsActive("rare_beta") || base.IsActive("rare_alpha") {
				t.Fatal("discovery leaked between tools or registries")
			}
			next := loop.buildToolDefs()
			if len(next) != len(defs)+1 {
				t.Fatalf("discovery should expose exactly one schema: %d -> %d", len(defs), len(next))
			}
			existing := make([]llm.ToolDef, 0, len(defs))
			for _, def := range next {
				if def.Name != "rare_alpha" {
					existing = append(existing, def)
				}
			}
			if !reflect.DeepEqual(defs, existing) {
				t.Fatal("discovery reordered existing prefix")
			}
		})
	}
}
