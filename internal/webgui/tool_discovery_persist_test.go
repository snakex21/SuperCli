package webgui

import (
	"context"
	"encoding/json"
	"testing"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

type discoveryContinuationProvider struct{ calls int }

func (p *discoveryContinuationProvider) Name() string         { return "echo-test" }
func (p *discoveryContinuationProvider) SupportsVision() bool { return false }
func (p *discoveryContinuationProvider) Complete(_ context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	ch := make(chan llm.Delta, 1)
	switch p.calls {
	case 0:
		ch <- llm.Delta{ToolCall: &llm.ToolCall{ID: "discover", Name: "tool_search", Arguments: `{"query":"fixture_records"}`}, FinishReason: "tool_calls"}
	case 2:
		ch <- llm.Delta{ToolCall: &llm.ToolCall{ID: "use", Name: "invoke_tool", Arguments: `{"tool":"fixture_records","args":{"items":["one"]}}`}, FinishReason: "tool_calls"}
	default:
		ch <- llm.Delta{Content: "Ready.", FinishReason: "stop"}
	}
	p.calls++
	close(ch)
	return ch, nil
}

func TestWebRecreatedLoopRestoresToolDiscovery(t *testing.T) {
	ctx := context.Background()
	eng, err := NewEngine(echoConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	p := &discoveryContinuationProvider{}
	eng.prov = p
	store, err := eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(eng.Home(), p.Name(), "")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, sess.ID)
	executions := 0
	for turn := 0; turn < 2; turn++ {
		history, err := store.ReadModelContext(ctx, sess.ID)
		if err != nil {
			t.Fatal(err)
		}
		l, err := eng.newLoopWithSession(history, writer)
		if err != nil {
			t.Fatal(err)
		}
		eng.diagnosticRegistry.MustRegister(tools.Tool{Name: "fixture_records", Description: "Process fixture records.", ReadOnly: true,
			Schema: `{"type":"object","properties":{"items":{"type":"array","items":{"type":"string"}}},"required":["items"]}`,
			Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				executions++
				return tools.Result{Text: "record count: 1"}, nil
			}})
		ch, err := l.Run(ctx, "Inspect the source code with the records tool.")
		if err != nil {
			t.Fatal(err)
		}
		for ev := range ch {
			switch e := ev.(type) {
			case agent.ErrorEvent:
				t.Fatal(e.Err)
			case agent.ToolResultEvent:
				if e.Err != nil {
					t.Fatal(e.Err)
				}
			}
		}
	}
	if p.calls != 4 || executions != 1 {
		t.Fatalf("provider calls=%d executions=%d", p.calls, executions)
	}
}
