package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestInvokeToolEligibilityRequiresFlatReadOnlySchema(t *testing.T) {
	flat := tools.Tool{Name: "read", Description: "read", ReadOnly: true, Schema: `{"type":"object","properties":{"path":{"type":"string"},"limit":{"type":"integer"}}}`}
	compact := tools.Tool{Name: "compact", Description: "read", ReadOnly: true, Schema: `{"path":{"type":"string"},"limit":{"type":"integer"}}`}
	nested := tools.Tool{Name: "nested", Description: "read", ReadOnly: true, Schema: `{"type":"object","properties":{"items":{"type":"array"}}}`}
	mutating := tools.Tool{Name: "write", Description: "write", Schema: flat.Schema}
	for _, tc := range []struct {
		name string
		tool tools.Tool
		want bool
	}{{"flat", flat, true}, {"compact", compact, true}, {"nested", nested, false}, {"mutating", mutating, false}} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDirectToolEligible(tc.tool); got != tc.want {
				t.Fatalf("eligible=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveInvokeToolCall_NativeAndSentinel(t *testing.T) {
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name: "lookup", Description: "lookup", ReadOnly: true,
		Schema: `{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer"}}}`,
		Fn:     func(_ context.Context, _ json.RawMessage) (tools.Result, error) { return tools.Result{Text: "ok"}, nil },
	})

	for _, args := range []string{
		`{"tool":"lookup","args":{"query":"alpha","limit":3}}`,
		`{"tool":"lookup","arg.query":"alpha","arg.limit":"3"}`,
	} {
		got, err := resolveInvokeToolCall(reg, llm.ToolCall{ID: "c1", Name: invokeToolName, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != "lookup" || got.ID != "c1" || !strings.Contains(got.Arguments, `"query":"alpha"`) {
			t.Fatalf("resolved = %+v", got)
		}
	}
}

func TestResolveInvokeToolCallRejectsMutationAndUnknownArgs(t *testing.T) {
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "write", Description: "write", Schema: `{"path":{"type":"string"}}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }})
	reg.MustRegister(tools.Tool{Name: "read", Description: "read", ReadOnly: true, Schema: `{"path":{"type":"string"}}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }})
	if _, err := resolveInvokeToolCall(reg, llm.ToolCall{Name: invokeToolName, Arguments: `{"tool":"write","arg.path":"x"}`}); err == nil || !strings.Contains(err.Error(), "requires tool_search") {
		t.Fatalf("mutation err = %v", err)
	}
	if _, err := resolveInvokeToolCall(reg, llm.ToolCall{Name: invokeToolName, Arguments: `{"tool":"read","arg.unknown":"x"}`}); err == nil || !strings.Contains(err.Error(), "not valid") {
		t.Fatalf("unknown arg err = %v", err)
	}
}

func TestInvokeToolSpecAdvertisesOnlyEligibleTools(t *testing.T) {
	reg := tools.NewRegistry()
	noop := func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{Text: "ok"}, nil }
	reg.MustRegister(tools.Tool{Name: "safe_read", Description: "safe", ReadOnly: true, Schema: `{"path":{"type":"string"}}`, Fn: noop})
	reg.MustRegister(tools.Tool{Name: "dangerous_write", Description: "danger", Schema: `{"path":{"type":"string"}}`, Fn: noop})
	spec := NewInvokeTool(reg).Spec()
	if !strings.Contains(spec.Description, "safe_read(path:string)") {
		t.Fatalf("eligible tool missing: %s", spec.Description)
	}
	if strings.Contains(spec.Description, "dangerous_write") {
		t.Fatalf("mutating tool leaked into direct catalog: %s", spec.Description)
	}
}
