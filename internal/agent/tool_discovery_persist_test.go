package agent

import (
	"context"
	"encoding/json"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

func TestDiscoveredToolSurvivesFreshLoop(t *testing.T) {
	ctx := context.Background()
	store, err := session.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sess, err := store.Create(t.TempDir(), "test", "")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, sess.ID)
	executions := 0
	registry := func() *tools.Registry {
		r := tools.NewRegistry()
		r.MustRegister(tools.Tool{Name: "fixture_records", Description: "Process fixture records.", ReadOnly: true,
			Schema: `{"type":"object","properties":{"items":{"type":"array","items":{"type":"string"}}},"required":["items"]}`,
			Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				executions++
				return tools.Result{Text: "record count: 1"}, nil
			}})
		r.MustRegister(tools.NewToolSearcher(r, nil).Spec())
		r.MarkAlwaysOn("tool_search")
		r.MustRegister(NewInvokeTool(r).Spec())
		r.MarkAlwaysOn("invoke_tool")
		return r
	}
	first := &stubProvider{name: "test", scripts: [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "find", Name: "tool_search", Arguments: `{"query":"fixture_records"}`}, FinishReason: "tool_calls"}},
		{{Content: "The tool is ready.", FinishReason: "stop"}},
	}}
	l, err := NewLoop(LoopConfig{Provider: first, Registry: registry(), Writer: writer, ThinTools: true, StableToolset: true, MaxSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := l.Run(ctx, "Find the records tool.")
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	history, err := store.ReadModelContext(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	second := &stubProvider{name: "test", scripts: [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "use", Name: "invoke_tool", Arguments: `{"tool":"fixture_records","args":{"items":["one"]}}`}, FinishReason: "tool_calls"}},
		{{Content: "Processed one record.", FinishReason: "stop"}},
	}}
	resumed, err := NewLoop(LoopConfig{Provider: second, Registry: registry(), Writer: writer, InitialMessages: history, ThinTools: true, StableToolset: true, MaxSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	ch, err = resumed.Run(ctx, "Use that tool on one record.")
	if err != nil {
		t.Fatal(err)
	}
	var failure string
	for ev := range ch {
		if r, ok := ev.(ToolResultEvent); ok && r.Err != nil {
			failure = r.Err.Error()
		}
	}
	if executions != 1 || failure != "" {
		t.Fatalf("continuation executed=%d, failure=%s", executions, failure)
	}
	if len(second.reqs) != 2 {
		t.Fatalf("continuation made %d model calls, want tool call + answer", len(second.reqs))
	}
}

// Optional metadata adds one read per loop and writes only changed discoveries.
type discoveryWriter struct {
	read, write int
	names       []string
}

func (w *discoveryWriter) AppendMessage(context.Context, llm.Message) error { return nil }
func (w *discoveryWriter) UpdateUsage(int, int) error                       { return nil }
func (w *discoveryWriter) ReadDiscoveredTools(context.Context) ([]string, error) {
	w.read++
	return append([]string(nil), w.names...), nil
}
func (w *discoveryWriter) SaveDiscoveredTools(_ context.Context, names []string) error {
	w.write++
	w.names = append([]string(nil), names...)
	return nil
}

func TestDiscoveryStateRestoresOnlyAvailableToolsAndAvoidsRepeatedIO(t *testing.T) {
	reg := tools.NewRegistry()
	for _, name := range []string{"available", "automatic", "new_tool"} {
		reg.MustRegister(tools.Tool{Name: name, Description: name, Schema: `{"type":"object"}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{Text: "ok"}, nil }})
	}
	reg.Activate("automatic")
	w := &discoveryWriter{names: []string{"unavailable", "available"}}
	l, err := NewLoop(LoopConfig{Provider: &stubProvider{name: "test"}, Registry: reg, Writer: w})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	l.restoreDiscoveredTools(ctx)
	l.restoreDiscoveredTools(ctx)
	if !reg.IsActive("available") || reg.IsActive("unavailable") {
		t.Fatal("restricted registry was bypassed")
	}
	l.persistDiscoveredTools(ctx) // drops the unavailable name from the new snapshot
	if w.read != 1 || w.write != 1 || len(w.names) != 1 || w.names[0] != "available" {
		t.Fatalf("state=%+v", w)
	}
	l.persistDiscoveredTools(ctx)
	l.persistDiscoveredTools(ctx)
	if w.write != 1 {
		t.Fatal("unchanged discovery causes repeated writes")
	}
	reg.ActivateDiscovered("new_tool")
	l.persistDiscoveredTools(ctx)
	if w.write != 2 {
		t.Fatal("new discovery not saved")
	}
}

func TestCanceledTurnRetainsCompletedDiscovery(t *testing.T) {
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "fixture", Description: "fixture", Schema: `{"type":"object"}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{Text: "ok"}, nil }})
	reg.MustRegister(tools.NewToolSearcher(reg, nil).Spec())
	reg.MarkAlwaysOn("tool_search")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &stubProvider{name: "test", scripts: [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "find", Name: "tool_search", Arguments: `{"query":"fixture"}`}, FinishReason: "tool_calls"}},
		{{Content: "done", FinishReason: "stop"}},
	}, onCalled: func(call int) {
		if call == 1 {
			cancel()
		}
	}}
	w := &discoveryWriter{}
	l, err := NewLoop(LoopConfig{Provider: p, Registry: reg, Writer: w})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := l.Run(ctx, "Find the fixture.")
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	if len(w.names) != 1 || w.names[0] != "fixture" {
		t.Fatalf("completed discovery lost on cancellation: %+v", w)
	}
}
