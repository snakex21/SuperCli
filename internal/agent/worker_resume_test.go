package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"supercli/internal/llm"
	"supercli/internal/tools"
	"testing"
)

func TestWorkerResumeUsesCurrentInvocationAndFinalReport(t *testing.T) {
	p := &stubProvider{name: "worker", scripts: [][]llm.Delta{
		{{Content: "first report"}, {FinishReason: "stop"}},
		{{Content: "I will inspect it."}, {ToolCall: &llm.ToolCall{ID: "read", Name: "probe", Arguments: `{}`}}, {FinishReason: "tool_calls"}},
		{{Content: "verified final report"}, {FinishReason: "stop"}},
	}}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "probe", Description: "probe", Schema: `{}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		return tools.Result{Text: "evidence"}, nil
	}})
	w := &Worker{ID: "worker-1", Agent: "general", Loop: makeLoop(t, p, reg, "")}
	old := make(chan Event, 20)
	_, err := runWorkerLoop(withWorkerInvocation(context.Background(), "parent-1", old), w, "first")
	if err != nil {
		t.Fatal(err)
	}
	oldCount := len(old)
	current := make(chan Event, 20)
	result, err := runWorkerLoop(withWorkerInvocation(context.Background(), "parent-2", current), w, "continue")
	if err != nil || result != "verified final report" {
		t.Fatalf("result=%q err=%v", result, err)
	}
	if len(old) != oldCount {
		t.Fatal("resume wrote to old UI stream")
	}
	if len(current) != 4 {
		t.Fatalf("events=%d", len(current))
	}
	kinds := []string{}
	for len(current) > 0 {
		ev := (<-current).(WorkerProgressEvent)
		if ev.ParentCallID != "parent-2" || ev.Run != 2 || ev.TaskID != w.ID {
			t.Fatalf("event=%+v", ev)
		}
		kinds = append(kinds, ev.Kind)
	}
	if !reflect.DeepEqual(kinds, []string{"started", "tool_call", "tool_result", "finished"}) {
		t.Fatal(kinds)
	}
}

func TestWorkerBusyDoesNotBlockFollowup(t *testing.T) {
	w := &Worker{ID: "busy"}
	w.runMu.Lock()
	defer w.runMu.Unlock()
	_, err := runWorkerLoop(context.Background(), w, "follow up")
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatal(err)
	}
}

func TestRestrictedRegistryDiscoveryIsLocalAndStable(t *testing.T) {
	base := tools.NewRegistry()
	for _, name := range []string{"zebra", "alpha", "task", "send_message"} {
		base.MustRegister(tools.Tool{Name: name, Description: name, Schema: `{"type":"object"}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }})
	}
	base.MustRegister(tools.NewToolSearcher(base, nil).Spec())
	base.MustRegister(NewInvokeTool(base).Spec())
	child := restrictedRegistry(base, nil)
	for i := 0; i < 20; i++ {
		if !reflect.DeepEqual(child.VisibleNames(), restrictedRegistry(base, nil).VisibleNames()) {
			t.Fatal("worker prefix order is unstable")
		}
	}
	res, err := child.Execute(context.Background(), "tool_search", json.RawMessage(`{"query":"zebra","limit":1}`))
	if err != nil || res.Err != nil {
		t.Fatalf("search=%+v %v", res, err)
	}
	if !child.IsActive("zebra") || base.IsActive("zebra") {
		t.Fatal("search activated the wrong registry")
	}
	call, err := resolveInvokeToolCall(child, llm.ToolCall{Name: invokeToolName, Arguments: `{"tool":"zebra","args":{}}`})
	if err != nil || call.Name != "zebra" {
		t.Fatalf("dispatch=%+v %v", call, err)
	}
	res, _ = child.Execute(context.Background(), "tool_search", json.RawMessage(`{"query":"task","limit":1}`))
	if strings.Contains(res.Text, `"name":"task"`) || strings.Contains(res.Text, `"name":"send_message"`) {
		t.Fatal("worker search leaked delegation tools")
	}
}

func TestContinuationBatchKeepsSameWorkerSequential(t *testing.T) {
	cases := []struct {
		calls    []llm.ToolCall
		parallel bool
	}{
		{[]llm.ToolCall{{Name: "send_message", Arguments: `{"to":"worker-1"}`}, {Name: "send_message", Arguments: `{"to":"worker-2"}`}}, true},
		{[]llm.ToolCall{{Name: "task"}, {Name: "send_message", Arguments: `{"to":"worker-1"}`}}, true},
		{[]llm.ToolCall{{Name: "send_message", Arguments: `{"to":"worker-1"}`}, {Name: "send_message", Arguments: `{"to":" worker-1 "}`}}, false},
		{[]llm.ToolCall{{Name: "task"}, {Name: "patch_file"}}, false},
	}
	for _, tc := range cases {
		if got := allTaskCalls(tc.calls); got != tc.parallel {
			t.Fatalf("calls=%+v parallel=%v", tc.calls, got)
		}
	}
}

func TestIndependentContinuationsFollowBackendParallelPolicy(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		t.Run(fmt.Sprintf("parallel=%v", parallel), func(t *testing.T) {
			var active, peak int32
			registry := concurrencyTaskRegistry(&active, &peak)
			task, _ := registry.Get("task")
			task.Name = "send_message"
			task.Schema = `{"type":"object","properties":{"to":{"type":"string"},"message":{"type":"string"}}}`
			registry.MustRegister(task)
			p := &stubProvider{name: "fixture", scripts: [][]llm.Delta{
				{{ToolCall: &llm.ToolCall{ID: "a", Name: "send_message", Arguments: `{"to":"worker-1","message":"continue"}`}}, {ToolCall: &llm.ToolCall{ID: "b", Name: "send_message", Arguments: `{"to":"worker-2","message":"continue"}`}}, {FinishReason: "tool_calls"}},
				{{Content: "done"}, {FinishReason: "stop"}},
			}}
			loop, err := NewLoop(LoopConfig{Provider: p, Registry: registry, TaskParallel: parallel})
			if err != nil {
				t.Fatal(err)
			}
			drainEvents(t, mustRun(t, loop, "continue both"))
			want := int32(1)
			if parallel {
				want = 2
			}
			if peak != want {
				t.Fatalf("peak=%d want=%d", peak, want)
			}
			var ids []string
			for _, m := range loop.Messages {
				if m.Role == llm.RoleTool {
					ids = append(ids, m.ToolCallID)
				}
			}
			if !reflect.DeepEqual(ids, []string{"a", "b"}) {
				t.Fatalf("result order=%v", ids)
			}
		})
	}
}
