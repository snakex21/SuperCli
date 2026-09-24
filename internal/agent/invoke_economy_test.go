package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// This provider reproduces the repair round-trip in recorded sessions: after
// a refused envelope, the model repeats exactly the same call directly.
type envelopeRepairProvider struct {
	first        llm.ToolCall
	direct       llm.ToolCall
	calls        int
	requestBytes int
	requests     [][]llm.Message
}

func (p *envelopeRepairProvider) Name() string { return "envelope-replay" }

func (p *envelopeRepairProvider) Complete(_ context.Context, msgs []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	p.calls++
	p.requests = append(p.requests, append([]llm.Message(nil), msgs...))
	wire, _ := json.Marshal(struct {
		Messages []llm.Message
		Tools    []llm.ToolDef
	}{msgs, defs})
	p.requestBytes += len(wire)
	ch := make(chan llm.Delta, 2)
	switch {
	case p.calls == 1:
		ch <- llm.Delta{ToolCall: &p.first, FinishReason: "tool_calls"}
	case p.calls == 2 && lastEnvelopeResultFailed(msgs):
		ch <- llm.Delta{ToolCall: &p.direct, FinishReason: "tool_calls"}
	default:
		ch <- llm.Delta{Content: "Finished.", FinishReason: "stop"}
	}
	close(ch)
	return ch, nil
}

func lastEnvelopeResultFailed(msgs []llm.Message) bool {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleTool {
			return strings.HasPrefix(msgs[i].Content, "error:")
		}
	}
	return false
}

func builtinEnvelopeFixture(name string) (tools.Tool, map[string]any) {
	if name == "remember" {
		return tools.NewRemember(nil).Spec(), map[string]any{"text": "Use table tests in this fixture.", "type": "decision"}
	}
	return tools.Tool{
		Name: "task", Description: "Delegate a fixture investigation",
		Schema: `{"type":"object","properties":{"agent":{"type":"string"},"prompt":{"type":"string"},"expect":{"type":"string"}},"required":["agent","prompt"]}`,
	}, map[string]any{"agent": "general", "prompt": "Inspect fixture.go", "expect": "One finding"}
}

func TestBuiltinInvokeAvoidsRepairTurn(t *testing.T) {
	for _, name := range []string{"remember", "task"} {
		for _, shape := range []string{"args", "arg fields", "flat"} {
			for _, thin := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/thin=%v", name, shape, thin), func(t *testing.T) {
					reg := tools.NewRegistry()
					spec, args := builtinEnvelopeFixture(name)
					wantArgs, _ := json.Marshal(args)
					executed, verified := 0, 0
					spec.Fn = func(_ context.Context, raw json.RawMessage) (tools.Result, error) {
						executed++
						if string(raw) != string(wantArgs) {
							t.Errorf("target arguments changed: %s; want %s", raw, wantArgs)
						}
						return tools.Result{Text: "fixture saved"}, nil
					}
					spec.Verify = func(tools.Result) tools.VerifyVerdict {
						verified++
						return tools.VerifyVerdict{OK: true}
					}
					reg.MustRegister(spec)
					reg.MarkAlwaysOn(name)
					reg.MustRegister(NewInvokeTool(reg).Spec())
					reg.MarkAlwaysOn(invokeToolName)
					envelope := map[string]any{"tool": name}
					switch shape {
					case "args":
						envelope["args"] = args
					case "arg fields":
						for k, v := range args {
							envelope["arg."+k] = v
						}
					case "flat":
						for k, v := range args {
							envelope[k] = v
						}
					}
					raw, _ := json.Marshal(envelope)
					p := &envelopeRepairProvider{
						first:  llm.ToolCall{ID: "wrapped", Name: invokeToolName, Arguments: string(raw)},
						direct: llm.ToolCall{ID: "repair", Name: name, Arguments: string(wantArgs)},
					}
					l, err := NewLoop(LoopConfig{Provider: p, Registry: reg, ThinTools: thin, StableToolset: true, MaxSteps: 4})
					if err != nil {
						t.Fatal(err)
					}
					drainEvents(t, mustRun(t, l, "Inspect the fixture source and save the finding."))
					t.Logf("requests=%d request_bytes=%d executions=%d verifications=%d", p.calls, p.requestBytes, executed, verified)
					if executed != 1 || verified != 1 {
						t.Fatalf("execution/verification=%d/%d, want exactly 1/1", executed, verified)
					}
					if p.calls != 2 {
						t.Fatalf("model requests=%d, want call plus final answer without a repair request", p.calls)
					}
					var called, returned bool
					for _, msg := range p.requests[1] {
						for _, call := range msg.ToolCalls {
							if call.ID == "wrapped" {
								called = call.Name == name && call.Arguments == string(wantArgs)
							}
						}
						if msg.ToolCallID == "wrapped" {
							returned = msg.Name == name && !strings.HasPrefix(msg.Content, "error:")
						}
					}
					if !called || !returned {
						t.Fatalf("target call/result pairing lost: call=%v result=%v", called, returned)
					}
				})
			}
		}
	}
}

func TestBuiltinInvokeKeepsAccessAndValidation(t *testing.T) {
	for _, name := range []string{"remember", "task"} {
		t.Run(name, func(t *testing.T) {
			reg := tools.NewRegistry()
			spec, args := builtinEnvelopeFixture(name)
			executed := 0
			spec.Fn = func(context.Context, json.RawMessage) (tools.Result, error) {
				executed++
				return tools.Result{Text: "ok"}, nil
			}
			reg.MustRegister(spec)
			reg.MustRegister(NewInvokeTool(reg).Spec())
			raw, _ := json.Marshal(map[string]any{"tool": name, "args": args})
			call := llm.ToolCall{ID: "gated", Name: invokeToolName, Arguments: string(raw)}
			if _, err := resolveInvokeToolCall(reg, call); err == nil {
				t.Fatal("hidden builtin bypassed activation")
			}
			reg.MarkAlwaysOn(name)
			call.Arguments = fmt.Sprintf(`{"tool":%q,"args":{}}`, name)
			resolved, err := resolveInvokeToolCall(reg, call)
			if err != nil {
				t.Fatal(err)
			}
			loop := &Loop{registry: reg}
			result := loop.invoke(context.Background(), resolved, make(chan Event, 8))
			if executed != 0 || !result.failed {
				t.Fatalf("invalid arguments reached target: %d %+v", executed, result)
			}
			call.Arguments = string(raw)
			resolved, err = resolveInvokeToolCall(reg, call)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			result = loop.invoke(ctx, resolved, make(chan Event, 8))
			if executed != 0 || !result.failed {
				t.Fatalf("canceled request executed: %d %+v", executed, result)
			}
		})
	}
}

func TestInvokeEnvelopeRejectsAmbiguousArguments(t *testing.T) {
	reg := tools.NewRegistry()
	spec, _ := builtinEnvelopeFixture("remember")
	spec.Fn = func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }
	reg.MustRegister(spec)
	reg.MarkAlwaysOn("remember")
	// Both values would be valid individually; choosing either would alter intent.
	for _, raw := range []string{
		`{"tool":"remember","args":{"text":"a"},"text":"b"}`,
		`{"tool":"remember","arg.text":"a","text":"b"}`,
		`{"tool":"remember","args":{"text":"a"},"arg.text":"b"}`,
	} {
		if _, err := resolveInvokeToolCall(reg, llm.ToolCall{Name: invokeToolName, Arguments: raw}); err == nil || !strings.Contains(err.Error(), "duplicate") {
			t.Fatalf("ambiguous envelope was not rejected: %s, %v", raw, err)
		}
	}
}

func TestWrappedWorkersRespectBackendConcurrency(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		t.Run(fmt.Sprintf("parallel=%v", parallel), func(t *testing.T) {
			var active, peak int32
			reg := concurrencyTaskRegistry(&active, &peak)
			reg.MarkAlwaysOn("task")
			reg.MustRegister(NewInvokeTool(reg).Spec())
			reg.MarkAlwaysOn(invokeToolName)
			p := &stubProvider{name: "fixture", scripts: [][]llm.Delta{
				{
					{ToolCall: &llm.ToolCall{ID: "worker-a", Name: invokeToolName, Arguments: `{"tool":"task","args":{"prompt":"a"}}`}},
					{ToolCall: &llm.ToolCall{ID: "worker-b", Name: invokeToolName, Arguments: `{"tool":"task","args":{"prompt":"b"}}`}},
				},
				{{Content: "Finished.", FinishReason: "stop"}},
			}}
			l, err := NewLoop(LoopConfig{Provider: p, Registry: reg, TaskParallel: parallel, MaxSteps: 3})
			if err != nil {
				t.Fatal(err)
			}
			drainEvents(t, mustRun(t, l, "Inspect the two source files using workers."))
			wantPeak := int32(1)
			if parallel {
				wantPeak = 2
			}
			if peak != wantPeak || active != 0 || p.calls != 2 {
				t.Fatalf("peak=%d active=%d model requests=%d; want peak=%d, active=0, requests=2", peak, active, p.calls, wantPeak)
			}
			var results []string
			for _, msg := range p.reqs[1] {
				if msg.Role == llm.RoleTool {
					if msg.Name != "task" || strings.HasPrefix(msg.Content, "error:") {
						t.Fatalf("worker result=%+v", msg)
					}
					results = append(results, msg.ToolCallID)
				}
			}
			if strings.Join(results, ",") != "worker-a,worker-b" {
				t.Fatalf("worker result order changed: %v", results)
			}
		})
	}
}

func TestInvokeCannotRecurseOrEscapeRestrictedRegistry(t *testing.T) {
	base := tools.NewRegistry()
	spec, _ := builtinEnvelopeFixture("remember")
	spec.Fn = func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }
	base.MustRegister(spec)
	base.MustRegister(tools.Tool{Name: "patch_file", Description: "edit", Schema: "{}",
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil },
	})
	restricted := OrchestratorRegistry(base)
	restricted.MustRegister(NewInvokeTool(restricted).Spec())
	for _, raw := range []string{
		`{"tool":"invoke_tool","args":{"tool":"remember","args":{"text":"x"}}}`,
		`{"tool":"patch_file","args":{}}`,
	} {
		if _, err := resolveInvokeToolCall(restricted, llm.ToolCall{Name: invokeToolName, Arguments: raw}); err == nil {
			t.Fatalf("invalid dispatch accepted: %s", raw)
		}
	}
}
