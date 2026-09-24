package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestDiscoveryAndInvokeCanShareOneModelResponse(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, mode := range []string{"together", "wrapped discovery", "separate", "reverse", "missing discovery", "invalid args", "verification failure"} {
			t.Run(fmt.Sprintf("thin=%v/%s", thin, mode), func(t *testing.T) {
				reg := tools.NewRegistry()
				executed, verified := 0, 0
				reg.MustRegister(tools.Tool{Name: "save_notes", Description: "save structured notes", Schema: `{"type":"object","properties":{"notes":{"type":"array","items":{"type":"string"}}},"required":["notes"]}`,
					Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
						executed++
						return tools.Result{Text: "notes saved"}, nil
					},
					Verify: func(tools.Result) tools.VerifyVerdict {
						verified++
						return tools.VerifyVerdict{OK: mode != "verification failure", Reason: "fixture verifier"}
					},
				})
				reg.MustRegister(tools.NewToolSearcher(reg, nil).Spec())
				reg.MustRegister(NewInvokeTool(reg).Spec())
				reg.MarkAlwaysOn("tool_search")
				reg.MarkAlwaysOn("invoke_tool")
				search := llm.Delta{ToolCall: &llm.ToolCall{ID: "discover", Name: "tool_search", Arguments: `{"query":"save_notes"}`}}
				invoke := llm.Delta{ToolCall: &llm.ToolCall{ID: "save", Name: "invoke_tool", Arguments: `{"tool":"save_notes","args":{"notes":["finding"]}}`}}
				scripts := [][]llm.Delta{{search, invoke}}
				switch mode {
				case "wrapped discovery":
					search.ToolCall.Name = invokeToolName
					search.ToolCall.Arguments = `{"tool":"tool_search","query":"save_notes"}`
				case "separate":
					scripts = [][]llm.Delta{{search}, {invoke}}
				case "reverse":
					scripts = [][]llm.Delta{{invoke, search}}
				case "missing discovery":
					search.ToolCall.Arguments = `{"query":"qqzz_absent_capability_90"}`
				case "invalid args":
					invoke.ToolCall.Arguments = `{"tool":"save_notes","args":{}}`
				}
				scripts = append(scripts, []llm.Delta{{Content: "Finished.", FinishReason: "stop"}})
				p := &stubProvider{name: "test", scripts: scripts}
				l, err := NewLoop(LoopConfig{Provider: p, Registry: reg, ThinTools: thin, StableToolset: true, MaxSteps: 5})
				if err != nil {
					t.Fatal(err)
				}
				events := drainEvents(t, mustRun(t, l, "Save the finding."))
				wantExecuted := 1
				if mode == "reverse" || mode == "missing discovery" || mode == "invalid args" {
					wantExecuted = 0
				}
				wantVerified := wantExecuted
				if mode == "invalid args" {
					wantVerified = 1
				} // The normal verifier also sees schema failures.
				if executed != wantExecuted || verified != wantVerified {
					t.Fatalf("target=%d verification=%d, want %d/%d", executed, verified, wantExecuted, wantVerified)
				}
				wantRequests := int32(2)
				if mode == "separate" {
					wantRequests = 3
				}
				if p.calls != wantRequests {
					t.Fatalf("model requests=%d, want %d", p.calls, wantRequests)
				}
				wantFailure := wantExecuted == 0 || mode == "verification failure"
				sawResult := false
				var callName string
				for _, m := range p.reqs[len(p.reqs)-1] {
					for _, call := range m.ToolCalls {
						if call.ID == "save" {
							callName = call.Name
						}
					}
					if m.ToolCallID == "save" {
						sawResult = true
						if m.Name != callName {
							t.Fatalf("call/result names differ: %q / %q", callName, m.Name)
						}
						if strings.HasPrefix(m.Content, "error:") != wantFailure {
							t.Fatalf("incorrect outcome: %s", m.Content)
						}
					}
				}
				if !sawResult {
					t.Fatal("missing result")
				}
				if wantExecuted == 1 {
					sawTarget := false
					for _, event := range events {
						if ev, ok := event.(ToolCallEvent); ok && ev.ID == "save" && ev.Name == "save_notes" {
							sawTarget = true
						}
					}
					if !sawTarget {
						t.Fatal("UI did not receive resolved target name")
					}
					wantDispatches := int64(1)
					if mode == "wrapped discovery" {
						wantDispatches = 2
					}
					if l.InvokeToolDispatches() != wantDispatches {
						t.Fatalf("dispatch count=%d", l.InvokeToolDispatches())
					}
				}
			})
		}
	}
}

func TestMixedBatchParallelReadsRespectCommandBarrier(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	reg := tools.NewRegistry()
	var doneBefore, doneAfter, startedBefore, startedAfter, command atomic.Int32
	readyBefore, readyAfter := make(chan struct{}), make(chan struct{})
	readFn := func(after bool) func(context.Context, json.RawMessage) (tools.Result, error) {
		return func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
			started, done, ready := &startedBefore, &doneBefore, readyBefore
			wantCommand := int32(0)
			if after {
				started, done, ready = &startedAfter, &doneAfter, readyAfter
				wantCommand = 1
			}
			if command.Load() != wantCommand {
				t.Error("read crossed command barrier")
			}
			if started.Add(1) == 2 {
				close(ready)
			}
			select {
			case <-ready:
			case <-ctx.Done():
				return tools.Result{Err: ctx.Err()}, nil
			}
			done.Add(1)
			return tools.Result{Text: "read done"}, nil
		}
	}
	reg.MustRegister(tools.Tool{Name: "before_read", Description: "read", ReadOnly: true, Schema: "{}", Fn: readFn(false)})
	reg.MustRegister(tools.Tool{Name: "after_read", Description: "read", ReadOnly: true, Schema: "{}", Fn: readFn(true)})
	reg.MustRegister(tools.Tool{Name: "command_barrier", Description: "command", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		if doneBefore.Load() != 2 || startedAfter.Load() != 0 {
			t.Error("command did not wait for preceding reads")
		}
		command.Store(1)
		return tools.Result{Text: "command done"}, nil
	}})
	l, err := NewLoop(LoopConfig{Provider: echoProvider("test"), Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	calls := []llm.ToolCall{
		{ID: "a", Name: "before_read", Arguments: "{}"}, {ID: "b", Name: "before_read", Arguments: "{}"},
		{ID: "c", Name: "command_barrier", Arguments: "{}"},
		{ID: "d", Name: "after_read", Arguments: "{}"}, {ID: "e", Name: "after_read", Arguments: "{}"},
	}
	ok, outcomes := l.invokeToolCalls(ctx, calls, make(chan Event, 20))
	if !ok || countFailures(outcomes) != 0 || doneAfter.Load() != 2 {
		t.Fatalf("ok=%v outcomes=%+v finished=%d", ok, outcomes, doneAfter.Load())
	}
	for i, m := range l.Messages {
		if m.ToolCallID != calls[i].ID {
			t.Fatalf("result order at %d: %s", i, m.ToolCallID)
		}
	}
}

func TestMixedBatchDelegationsRespectBackendPolicy(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		t.Run(fmt.Sprintf("parallel=%v", parallel), func(t *testing.T) {
			var active, maxActive int32
			reg := concurrencyTaskRegistry(&active, &maxActive)
			reg.MustRegister(tools.Tool{Name: "read_summary", Description: "read", ReadOnly: true, Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				if atomic.LoadInt32(&active) != 0 {
					t.Error("read raced delegated writes")
				}
				return tools.Result{Text: "read done"}, nil
			}})
			l, err := NewLoop(LoopConfig{Provider: echoProvider("test"), Registry: reg, TaskParallel: parallel})
			if err != nil {
				t.Fatal(err)
			}
			calls := []llm.ToolCall{
				{ID: "a", Name: "task", Arguments: `{"prompt":"a"}`},
				{ID: "b", Name: "task", Arguments: `{"prompt":"b"}`},
				{ID: "c", Name: "read_summary", Arguments: "{}"},
			}
			ok, outcomes := l.invokeToolCalls(context.Background(), calls, make(chan Event, 20))
			if !ok || countFailures(outcomes) != 0 {
				t.Fatalf("batch failed: %+v", outcomes)
			}
			want := int32(1)
			if parallel {
				want = 2
			}
			if maxActive != want {
				t.Fatalf("concurrent workers=%d, want %d", maxActive, want)
			}
			for i, m := range l.Messages {
				if m.ToolCallID != calls[i].ID {
					t.Fatal("result order changed")
				}
			}
		})
	}
}

func TestMixedBatchSameWorkerInstructionsStayOrdered(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	reg := tools.NewRegistry()
	var active, finished [2]atomic.Int32
	var started [2]atomic.Int32
	ready := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	reg.MustRegister(tools.Tool{Name: "send_message", Description: "continue worker", Schema: "{}", Fn: func(ctx context.Context, raw json.RawMessage) (tools.Result, error) {
		var args struct {
			To   string
			Step int
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return tools.Result{Err: err}, nil
		}
		index := 0
		if args.To == "b" {
			index = 1
		}
		if active[index].Add(1) != 1 {
			t.Error("two instructions overlapped on one worker")
		}
		defer active[index].Add(-1)
		if finished[index].Load() != int32(args.Step-1) {
			t.Error("worker instructions reordered")
		}
		if started[args.Step-1].Add(1) == 2 {
			close(ready[args.Step-1])
		}
		select {
		case <-ready[args.Step-1]:
		case <-ctx.Done():
			return tools.Result{Err: ctx.Err()}, nil
		}
		finished[index].Store(int32(args.Step))
		return tools.Result{Text: "worker done"}, nil
	}})
	reg.MustRegister(tools.Tool{Name: "summary_read", Description: "read", ReadOnly: true, Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		if finished[0].Load() != 2 || finished[1].Load() != 2 {
			t.Error("summary ran before workers finished")
		}
		return tools.Result{Text: "summary"}, nil
	}})
	l, err := NewLoop(LoopConfig{Provider: echoProvider("test"), Registry: reg, TaskParallel: true})
	if err != nil {
		t.Fatal(err)
	}
	var calls []llm.ToolCall
	for step := 1; step <= 2; step++ {
		for _, id := range []string{"a", "b"} {
			raw, _ := json.Marshal(map[string]any{"to": id, "step": step})
			calls = append(calls, llm.ToolCall{ID: fmt.Sprintf("%s%d", id, step), Name: "send_message", Arguments: string(raw)})
		}
	}
	calls = append(calls, llm.ToolCall{ID: "summary", Name: "summary_read", Arguments: "{}"})
	ok, outcomes := l.invokeToolCalls(ctx, calls, make(chan Event, 24))
	if !ok || countFailures(outcomes) != 0 {
		t.Fatalf("batch failed: %+v", outcomes)
	}
	for i, m := range l.Messages {
		if m.ToolCallID != calls[i].ID {
			t.Fatal("history order changed")
		}
	}
}

// Artificial 5 ms I/O isolates scheduling from model speed/network latency.
// Both paths execute the same tools and append the same ordered results.
func BenchmarkMixedBatchExecution(b *testing.B) {
	for _, grouped := range []bool{false, true} {
		name := "previous_sequential"
		if grouped {
			name = "grouped_reads"
		}
		b.Run(name, func(b *testing.B) {
			reg := tools.NewRegistry()
			work := func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
				select {
				case <-time.After(5 * time.Millisecond):
					return tools.Result{Text: "ok"}, nil
				case <-ctx.Done():
					return tools.Result{Err: ctx.Err()}, nil
				}
			}
			reg.MustRegister(tools.Tool{Name: "benchmark_read", Description: "read", ReadOnly: true, Schema: "{}", Fn: work})
			reg.MustRegister(tools.Tool{Name: "benchmark_command", Description: "command", Schema: "{}", Fn: work})
			var calls []llm.ToolCall
			for i := 0; i < 9; i++ {
				name := "benchmark_read"
				if i == 4 {
					name = "benchmark_command"
				}
				calls = append(calls, llm.ToolCall{ID: fmt.Sprint(i), Name: name, Arguments: "{}"})
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				l, err := NewLoop(LoopConfig{Provider: echoProvider("test"), Registry: reg})
				if err != nil {
					b.Fatal(err)
				}
				events := make(chan Event, 32)
				var ok bool
				var outcomes []callOutcome
				if grouped {
					ok, outcomes = l.invokeToolCalls(context.Background(), calls, events)
				} else {
					ok, outcomes = l.invokeToolCallsSequential(context.Background(), calls, events)
				}
				if !ok || countFailures(outcomes) != 0 || len(l.Messages) != len(calls) {
					b.Fatal("incomplete batch")
				}
			}
		})
	}
}
