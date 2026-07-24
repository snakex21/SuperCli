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
		`{"tool":"lookup","args":"{\"query\":\"alpha\",\"limit\":3}"}`,
		`{"tool":"lookup","args":"query: alpha, limit: 3"}`,
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

func TestResolveInvokeToolCallRequiresActivationForMutation(t *testing.T) {
	reg := tools.NewRegistry()
	executed := false
	reg.MustRegister(tools.Tool{Name: "write", Description: "write", Schema: `{"path":{"type":"string"}}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		executed = true
		return tools.Result{Text: "written"}, nil
	}})
	reg.MustRegister(tools.Tool{Name: "read", Description: "read", ReadOnly: true, Schema: `{"path":{"type":"string"}}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }})
	if _, err := resolveInvokeToolCall(reg, llm.ToolCall{Name: invokeToolName, Arguments: `{"tool":"write","arg.path":"x"}`}); err == nil ||
		!strings.Contains(err.Error(), "tool_search once") || !strings.Contains(err.Error(), "not active") {
		t.Fatalf("mutation err = %v", err)
	}
	if executed {
		t.Fatal("dispatcher executed mutation before activation")
	}

	reg.Activate("write")
	resolved, err := resolveInvokeToolCall(reg, llm.ToolCall{Name: invokeToolName, Arguments: `{"tool":"write","arg.path":"x"}`})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Name != "write" || executed {
		t.Fatalf("resolved=%+v executed=%v; dispatcher must only rewrite", resolved, executed)
	}
	result, err := reg.Execute(context.Background(), resolved.Name, json.RawMessage(resolved.Arguments))
	if err != nil || result.Text != "written" || !executed {
		t.Fatalf("normal registry execution result=%+v err=%v executed=%v", result, err, executed)
	}

	if _, err := resolveInvokeToolCall(reg, llm.ToolCall{Name: invokeToolName, Arguments: `{"tool":"read","arg.unknown":"x"}`}); err == nil || !strings.Contains(err.Error(), "not valid") {
		t.Fatalf("unknown arg err = %v", err)
	}
}

func TestResolveInvokeToolCallRequiresActivationForComplexReadOnly(t *testing.T) {
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name: "nested_read", Description: "nested", ReadOnly: true,
		Schema: `{"type":"object","properties":{"filter":{"type":"object","properties":{"tags":{"type":"array","items":{"type":"string"}}}}}}`,
		Fn:     func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil },
	})
	call := llm.ToolCall{Name: invokeToolName, Arguments: `{"tool":"nested_read","args":{"filter":{"tags":["a","b"]}}}`}
	if _, err := resolveInvokeToolCall(reg, call); err == nil || !strings.Contains(err.Error(), "not active") {
		t.Fatalf("complex inactive err = %v", err)
	}
	reg.Activate("nested_read")
	got, err := resolveInvokeToolCall(reg, call)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "nested_read" || !strings.Contains(got.Arguments, `"tags":["a","b"]`) {
		t.Fatalf("resolved = %+v", got)
	}
}

func TestActivatedInvokeToolUsesNormalLoopVerification(t *testing.T) {
	reg := tools.NewRegistry()
	verified := false
	reg.MustRegister(tools.Tool{
		Name: "guarded_write", Description: "guarded write",
		Schema: `{"type":"object","properties":{"changes":{"type":"array","items":{"type":"object"}}}}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "target ran"}, nil
		},
		Verify: func(tools.Result) tools.VerifyVerdict {
			verified = true
			return tools.VerifyVerdict{OK: false, Reason: "test verifier rejected output"}
		},
	})
	reg.Activate("guarded_write")
	loop := &Loop{registry: reg}
	calls := loop.resolveInvokeToolCalls([]llm.ToolCall{{
		ID: "guarded-1", Name: invokeToolName,
		Arguments: `{"tool":"guarded_write","args":{"changes":[{"path":"a.txt"}]}}`,
	}})
	if len(calls) != 1 || calls[0].Name != "guarded_write" {
		t.Fatalf("calls were not rewritten to target: %+v", calls)
	}
	result := loop.invoke(context.Background(), calls[0], make(chan Event, 4))
	if !verified || !result.failed || len(result.followUps) != 1 ||
		!strings.Contains(result.followUps[0].Content, "test verifier rejected output") {
		t.Fatalf("normal verifier path not preserved: verified=%v result=%+v", verified, result)
	}
}

func TestResolveInvokeToolCallGoalControlEnvelope(t *testing.T) {
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "goal", Description: "goal", Schema: `{"type":"object"}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }})
	for _, args := range []string{
		`{"tool":"goal","action":"complete_task","task_seq":"1"}`,
		`{"tool":"goal","args":{"action":"verify","passed":true,"text":"tests pass"}}`,
		`{"tool":"goal","arg.action":"mark_done"}`,
	} {
		got, err := resolveInvokeToolCall(reg, llm.ToolCall{ID: "g1", Name: invokeToolName, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != "goal" || got.ID != "g1" || !json.Valid([]byte(got.Arguments)) || !strings.Contains(got.Arguments, `"action"`) {
			t.Fatalf("resolved = %+v", got)
		}
	}
}

func TestInvokeToolSpecAdvertisesOnlyEligibleTools(t *testing.T) {
	reg := tools.NewRegistry()
	noop := func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{Text: "ok"}, nil }
	reg.MustRegister(tools.Tool{Name: "safe_read", Description: "safe", ReadOnly: true, Schema: `{"path":{"type":"string"}}`, Fn: noop})
	reg.MustRegister(tools.Tool{Name: "dangerous_write", Description: "danger", Schema: `{"path":{"type":"string"}}`, Fn: noop})
	spec := NewInvokeTool(reg).Spec()
	if spec.ReadOnly {
		t.Fatal("dispatcher that can resolve mutations must not advertise ReadOnly")
	}
	if !strings.Contains(spec.Description, "safe_read(path:string)") {
		t.Fatalf("eligible tool missing: %s", spec.Description)
	}
	if strings.Contains(spec.Description, "dangerous_write") {
		t.Fatalf("mutating tool leaked into direct catalog: %s", spec.Description)
	}
}
