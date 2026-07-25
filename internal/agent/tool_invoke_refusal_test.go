package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// The largest single loss in the tool-call forensics: 7 invoke_tool envelopes
// carrying whole file bodies, 8183 tokens and 139-455 s of local generation,
// all refused with "call tool_search once to activate write_file, then retry
// invoke_tool" — an instruction to generate the same file a second time. The
// refusal must instead name a tool the model can call in the very next turn.

func refusalRegistry(t *testing.T, visible ...string) *tools.Registry {
	t.Helper()
	reg := tools.NewRegistry()
	noop := func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }
	for _, name := range []string{"write_file", "edit_line", "web_search"} {
		reg.MustRegister(tools.Tool{
			Name: name, Description: name,
			Schema: `{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}}}`,
			Fn:     noop,
		})
	}
	for _, name := range visible {
		reg.MarkAlwaysOn(name)
	}
	return reg
}

func refuse(t *testing.T, reg *tools.Registry, args string) string {
	t.Helper()
	_, err := resolveInvokeToolCall(reg, llm.ToolCall{Name: invokeToolName, Arguments: args})
	if err == nil {
		t.Fatal("expected refusal")
	}
	return err.Error()
}

func TestInvokeToolRefusal_WriteFilePointsAtReachableCoreTools(t *testing.T) {
	reg := refusalRegistry(t)
	msg := refuse(t, reg, `{"tool":"write_file","args":{"path":"app.js","content":"...2000 lines..."}}`)

	for _, want := range []string{"create_file", "patch_file", "will not overwrite"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("refusal does not name the reachable tool (%q missing): %s", want, msg)
		}
	}
	// The two instructions that cost the file a second time.
	if strings.Contains(msg, "retry invoke_tool") {
		t.Fatalf("refusal still sends the payload back through the envelope: %s", msg)
	}
	if strings.Contains(msg, "call tool_search") {
		t.Fatalf("refusal still spends a turn on tool_search for a tool that is always available: %s", msg)
	}
	if !strings.Contains(msg, "Do not resend the payload") {
		t.Fatalf("refusal does not warn against resending the payload: %s", msg)
	}
}

func TestInvokeToolRefusal_LegacyLineEditorsPointAtPatchFile(t *testing.T) {
	reg := refusalRegistry(t)
	msg := refuse(t, reg, `{"tool":"edit_line","args":{"path":"a.go","content":"x"}}`)
	if !strings.Contains(msg, "patch_file directly") {
		t.Fatalf("refusal does not redirect to patch_file: %s", msg)
	}
	if strings.Contains(msg, "call tool_search") {
		t.Fatalf("refusal spends a turn on tool_search: %s", msg)
	}
}

// A tool the model can already see needs no activation turn at all — just call
// it, rather than wrapping it.
func TestInvokeToolRefusal_VisibleTargetIsCalledDirectly(t *testing.T) {
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name: "guarded", Description: "guarded",
		Schema: `{"type":"object","properties":{"changes":{"type":"array"}}}`,
		Fn:     func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil },
	})
	reg.MarkAlwaysOn("guarded")
	msg := refuse(t, reg, `{"tool":"guarded","args":{"changes":[]}}`)
	if !strings.Contains(msg, "Call guarded directly as a normal tool call with the same arguments") {
		t.Fatalf("refusal does not point at a direct call: %s", msg)
	}
	if strings.Contains(msg, "call tool_search") {
		t.Fatalf("visible tool must not need activation: %s", msg)
	}
}

// A genuinely dormant tool still needs tool_search — there is no other way to
// reach it — but the retry must be a direct call, not another envelope.
func TestInvokeToolRefusal_DormantTailKeepsActivationButNotTheEnvelope(t *testing.T) {
	reg := refusalRegistry(t)
	msg := refuse(t, reg, `{"tool":"web_search","args":{"path":"q"}}`)
	if !strings.Contains(msg, "tool_search once to activate web_search") {
		t.Fatalf("activation path lost: %s", msg)
	}
	if !strings.Contains(msg, "then call web_search directly instead of through invoke_tool") {
		t.Fatalf("refusal still routes the retry through the envelope: %s", msg)
	}
	if strings.Contains(msg, "retry invoke_tool") {
		t.Fatalf("refusal still says retry invoke_tool: %s", msg)
	}
}

// Refusals must stay refusals: nothing is dispatched, nothing is counted.
func TestInvokeToolRefusal_DoesNotDispatchOrCount(t *testing.T) {
	reg := refusalRegistry(t)
	l := &Loop{registry: reg}
	calls := l.resolveInvokeToolCalls([]llm.ToolCall{{
		ID: "w1", Name: invokeToolName,
		Arguments: `{"tool":"write_file","args":{"path":"a.js","content":"x"}}`,
	}})
	if calls[0].Name != invokeToolName {
		t.Fatalf("refused envelope was rewritten: %+v", calls[0])
	}
	if got := l.InvokeToolDispatches(); got != 0 {
		t.Fatalf("failed dispatch counted: %d", got)
	}
	if l.invokeDispatchStep != 0 {
		t.Fatalf("per-step count = %d, want 0", l.invokeDispatchStep)
	}
}

// The counter exists because a successful rewrite leaves no other trace: the
// call is recorded under the target's name.
func TestInvokeToolDispatchCounterRisesOnSuccess(t *testing.T) {
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name: "lookup", Description: "lookup", ReadOnly: true,
		Schema: `{"type":"object","properties":{"query":{"type":"string"}}}`,
		Fn:     func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil },
	})
	l := &Loop{registry: reg}

	calls := l.resolveInvokeToolCalls([]llm.ToolCall{
		{ID: "a", Name: invokeToolName, Arguments: `{"tool":"lookup","args":{"query":"x"}}`},
		{ID: "b", Name: "lookup", Arguments: `{"query":"y"}`},
		{ID: "c", Name: invokeToolName, Arguments: `{"tool":"lookup","arg.query":"z"}`},
	})
	for _, c := range calls {
		if c.Name != "lookup" {
			t.Fatalf("call %s not resolved to target: %+v", c.ID, c)
		}
	}
	if got := l.InvokeToolDispatches(); got != 2 {
		t.Fatalf("dispatch count = %d, want 2 (the plain call must not count)", got)
	}
	if l.invokeDispatchStep != 2 {
		t.Fatalf("per-step count = %d, want 2", l.invokeDispatchStep)
	}

	// The per-step count resets; the session total accumulates.
	l.resolveInvokeToolCalls([]llm.ToolCall{{ID: "d", Name: "lookup", Arguments: `{"query":"w"}`}})
	if l.invokeDispatchStep != 0 {
		t.Fatalf("per-step count not reset: %d", l.invokeDispatchStep)
	}
	if got := l.InvokeToolDispatches(); got != 2 {
		t.Fatalf("session total changed on a non-dispatch step: %d", got)
	}
}
