package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

func TestWorkerRetainsOutputWithoutSharingConversationWriter(t *testing.T) {
	ctx := context.Background()
	store, err := session.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sess, err := store.Create("project", "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, sess.ID)
	reg := tools.NewRegistry()
	full := strings.Repeat("worker evidence ", 2000)
	reg.MustRegister(tools.Tool{Name: "fixture_log", Description: "fixture", Schema: "{}", ReadOnly: true, Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{Text: full}, nil }})
	reg.MarkAlwaysOn("fixture_log")
	provider := &outputReplayProvider{stubProvider: &stubProvider{name: "fixture", scripts: [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "inspect", Name: "fixture_log", Arguments: "{}"}, FinishReason: "tool_calls"}},
		{{Content: "Completed. Saved handle=out_000001", FinishReason: "stop"}},
	}}}
	parent, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, Writer: writer})
	if err != nil {
		t.Fatal(err)
	}
	specs := NewSubAgentRegistry()
	MustRegisterAll(specs, BuiltinSubAgents())
	task, err := NewAgentTool(specs, parent, reg, provider, nil, NewLoop)
	if err != nil {
		t.Fatal(err)
	}
	result, err := task.execute(ctx, json.RawMessage(`{"prompt":"Inspect evidence."}`))
	if err != nil || result.Err != nil {
		t.Fatalf("worker: %v %+v", err, result)
	}
	handle := handleInOutput(result.Text)
	if handle == "" {
		t.Fatal("worker omitted reference")
	}
	if got, err := writer.ReadToolOutput(ctx, handle); err != nil || got != full {
		t.Fatalf("worker lost persisted evidence: %v", err)
	}
	child, ok := task.Workers.Get("worker-1")
	if !ok || child.Loop.writer != nil {
		t.Fatal("child acquired parent conversation writer")
	}
	history, err := store.ReadMessages(ctx, sess.ID)
	if err != nil || len(history) != 0 {
		t.Fatalf("worker polluted parent transcript: messages=%d err=%v", len(history), err)
	}
	raw, _ := json.Marshal(map[string]any{"handle": handle, "query": "worker evidence"})
	read := parent.invoke(ctx, llm.ToolCall{ID: "read-saved", Name: "read_output", Arguments: string(raw)}, make(chan Event, 4))
	if read.failed || len(read.followUps) != 1 || !strings.Contains(read.followUps[0].Content, "worker evidence") {
		t.Fatalf("parent cannot inspect worker reference: %+v", read)
	}
}
