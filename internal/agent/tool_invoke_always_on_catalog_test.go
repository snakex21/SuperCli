package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// Live regression (web GUI, gpt-5.4-nano, "wygeneruj obrazek kota w
// kapeluszu", image generation enabled): in stable thin mode generate_image is
// an always-on tool advertised in the catalog with "call the target through
// invoke_tool". invoke_tool refused it ("call generate_image directly"), but
// the tool is not in the native tools array, so the model retried the same
// envelope and told the user it cannot generate images. A configured
// always-on catalog tool must dispatch through invoke_tool as advertised; the
// target still validates its own arguments.
func TestInvokeToolDispatchesAlwaysOnCatalogToolInStableThinMode(t *testing.T) {
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name: "generate_image", Description: "Generate one new image from a text prompt.",
		Schema: `{"type":"object","properties":{"prompt":{"type":"string"},"parameters":{"type":"object"}},"required":["prompt"]}`,
		Fn:     func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil },
	})
	reg.MustRegister(tools.Tool{
		Name: "dormant_mutator", Description: "x",
		Schema: `{"type":"object","properties":{"changes":{"type":"array"}}}`,
		Fn:     func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil },
	})
	reg.MarkAlwaysOn("generate_image")
	l := &Loop{registry: reg, route: RouteCoordinator, thinTools: true, stableToolset: true}
	if pre := l.thinToolsPreamble(); !strings.Contains(pre, "through invoke_tool") || !strings.Contains(pre, "generate_image") {
		t.Fatalf("catalog no longer advertises generate_image through invoke_tool:\n%s", pre)
	}
	calls := l.resolveInvokeToolCalls([]llm.ToolCall{
		{ID: "a", Name: invokeToolName, Arguments: `{"tool":"generate_image","prompt":"kot w kapeluszu"}`},
		{ID: "b", Name: invokeToolName, Arguments: `{"tool":"dormant_mutator","args":{"changes":[]}}`},
	})
	if calls[0].Name != "generate_image" || !strings.Contains(calls[0].Arguments, "kot w kapeluszu") {
		t.Fatalf("advertised always-on tool was not dispatched: %+v", calls[0])
	}
	if calls[1].Name != invokeToolName {
		t.Fatalf("a dormant mutating tool must still need tool_search activation: %+v", calls[1])
	}
	// Outside stable thin mode the tool carries its own native schema and the
	// existing "call it directly" contract is unchanged.
	dyn := &Loop{registry: reg, route: RouteCoordinator, thinTools: true}
	if got := dyn.resolveInvokeToolCalls([]llm.ToolCall{{ID: "c", Name: invokeToolName, Arguments: `{"tool":"generate_image","prompt":"x"}`}}); got[0].Name != invokeToolName {
		t.Fatalf("dynamic mode changed: %+v", got[0])
	}
}

// Live: the model wrapped the target as "functions.generate_image" inside the
// envelope and got "unknown tool". Direct calls already accept that wrapper.
func TestInvokeToolAcceptsFunctionsPrefixedTarget(t *testing.T) {
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name: "lookup", Description: "lookup", ReadOnly: true,
		Schema: `{"type":"object","properties":{"query":{"type":"string"}}}`,
		Fn:     func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil },
	})
	for _, name := range []string{"functions.lookup", "tools.lookup"} {
		got, err := resolveInvokeToolCall(reg, llm.ToolCall{Name: invokeToolName, Arguments: `{"tool":"` + name + `","args":{"query":"x"}}`})
		if err != nil || got.Name != "lookup" {
			t.Fatalf("%s: got %+v err=%v", name, got, err)
		}
	}
	if _, err := resolveInvokeToolCall(reg, llm.ToolCall{Name: invokeToolName, Arguments: `{"tool":"functions.missing"}`}); err == nil {
		t.Fatal("unknown prefixed tool must still be refused")
	}
}
