package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"supercli/internal/llm"
	"supercli/internal/tools"
	"testing"
)

// Keep the previous coordinator builder as an independent wire-format oracle.
func previousCoordinatorDefs(l *Loop) []llm.ToolDef {
	var schema, tail []tools.Tool
	for _, t := range l.registry.Visible() {
		if l.thinTools && !l.isSchemaCore(t.Name) {
			wordTurnTool := (t.Name == "read_docx" || t.Name == "edit_docx") && l.isActivated(t.Name)
			if !wordTurnTool && (l.stableToolset || !l.isActivated(t.Name)) {
				tail = append(tail, t)
				continue
			}
		}
		schema = append(schema, t)
	}
	var defs []llm.ToolDef
	for _, t := range schema {
		defs = append(defs, llm.ToolDef{Name: t.Name, Description: t.Description, Schema: t.Schema})
	}
	return defs
}

func TestToolDefinitionAssemblyParity(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, stable := range []bool{false, true} {
			for _, orchestrator := range []bool{false, true} {
				t.Run(fmt.Sprintf("thin=%v/stable=%v/orchestrator=%v", thin, stable, orchestrator), func(t *testing.T) {
					reg := tools.NewRegistry()
					for _, name := range []string{"tool_search", "task", "read_lines", "edit_docx", "read_docx", "extension_tool", "dormant_tool"} {
						reg.MustRegister(tools.Tool{Name: name, Description: "description " + name, Schema: `{"type":"object","properties":{"query":{"type":"string"}}}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }})
						if name != "dormant_tool" {
							reg.MarkAlwaysOn(name)
						}
					}
					l := &Loop{registry: reg, route: RouteCoordinator, thinTools: thin, stableToolset: stable, orchestrator: orchestrator}
					check := func() {
						t.Helper()
						got, want := l.buildToolDefs(), previousCoordinatorDefs(l)
						if !reflect.DeepEqual(got, want) {
							t.Fatalf("got=%+v want=%+v", got, want)
						}
						a, _ := json.Marshal(got)
						b, _ := json.Marshal(want)
						if string(a) != string(b) {
							t.Fatalf("wire bytes differ: %s %s", a, b)
						}
					}
					check()
					reg.Activate("extension_tool", "dormant_tool", "read_docx", "edit_docx")
					check()
					snapshot := l.buildToolDefs()
					saved := append([]llm.ToolDef(nil), snapshot...)
					reg.Deactivate("extension_tool", "dormant_tool", "read_docx", "edit_docx")
					check()
					if !reflect.DeepEqual(snapshot, saved) {
						t.Fatal("later calls mutated an earlier request")
					}
					reg.ResetVisibility()
					check()
					l.finalReplyOnly = true
					if l.buildToolDefs() != nil {
						t.Fatal("final-only request exposed tools")
					}
				})
			}
		}
	}
	// No tools must remain nil, preserving the old wire encoding (null vs []).
	l := &Loop{registry: tools.NewRegistry(), route: RouteCoordinator}
	if got := l.buildToolDefs(); got != nil {
		t.Fatalf("empty tools=%#v", got)
	}
}
