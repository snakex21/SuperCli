package agent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// recordingWriter is a test double for SessionWriter that
// records every message in order. Safe for concurrent use.
type recordingWriter struct {
	mu       sync.Mutex
	messages []llm.Message
	inTok    int
	outTok   int
}

func (r *recordingWriter) AppendMessage(ctx context.Context, msg llm.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, msg)
	return nil
}

func (r *recordingWriter) UpdateUsage(in, out int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inTok += in
	r.outTok += out
	return nil
}

func makeScriptedProvider(text string) *stubProvider {
	return &stubProvider{
		name: "test",
		scripts: [][]llm.Delta{{
			{Content: text},
			{FinishReason: "stop", Usage: &llm.Usage{Input: 3, Output: 5, Total: 8}},
		}},
	}
}

func emptyRegistry() *tools.Registry { return tools.NewRegistry() }

func TestLoop_PersistsUserAndAssistant(t *testing.T) {
	prov := makeScriptedProvider("echo back")
	reg := emptyRegistry()
	w := &recordingWriter{}
	loop, err := NewLoop(LoopConfig{
		Provider: prov,
		Registry: reg,
		Writer:   w,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	events, err := loop.Run(context.Background(), "hi there")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for range events {
		// drain
	}
	if len(w.messages) != 2 {
		t.Fatalf("writer.messages = %d, want 2 (user + assistant)", len(w.messages))
	}
	if w.messages[0].Role != llm.RoleUser || w.messages[0].Content != "hi there" {
		t.Errorf("user msg = %+v", w.messages[0])
	}
	if w.messages[1].Role != llm.RoleAssistant {
		t.Errorf("second msg = %+v", w.messages[1])
	}
}

func TestLoop_NoWriter_NoPanic(t *testing.T) {
	prov := makeScriptedProvider("ok")
	reg := emptyRegistry()
	loop, _ := NewLoop(LoopConfig{Provider: prov, Registry: reg})
	events, _ := loop.Run(context.Background(), "hi")
	for range events {
	}
}

func TestLoop_PersistsToolResult(t *testing.T) {
	tool := tools.Tool{
		Name: "echo",
		Fn: func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "echoed!"}, nil
		},
	}
	// Script: turn 1 = tool call, turn 2 = final text
	prov := &stubProvider{
		name: "test",
		scripts: [][]llm.Delta{
			{
				{ToolCall: &llm.ToolCall{ID: "c1", Name: "echo", Arguments: `{}`}},
				{FinishReason: "tool_calls", Usage: &llm.Usage{Input: 5, Output: 5, Total: 10}},
			},
			{
				{Content: "done"},
				{FinishReason: "stop", Usage: &llm.Usage{Input: 7, Output: 3, Total: 10}},
			},
		},
	}
	reg := emptyRegistry()
	reg.Register(tool)
	w := &recordingWriter{}
	loop, _ := NewLoop(LoopConfig{Provider: prov, Registry: reg, Writer: w, MaxSteps: 3})
	events, _ := loop.Run(context.Background(), "call echo")
	for range events {
	}
	// Expected: user, assistant (with tool call), tool (result), assistant (final)
	if len(w.messages) != 4 {
		t.Fatalf("messages = %d, want 4", len(w.messages))
	}
	if w.messages[2].Role != llm.RoleTool {
		t.Errorf("messages[2].Role = %q", w.messages[2].Role)
	}
	if w.messages[2].ToolCallID != "c1" {
		t.Errorf("messages[2].ToolCallID = %q", w.messages[2].ToolCallID)
	}
	if w.messages[3].Role != llm.RoleAssistant {
		t.Errorf("messages[3].Role = %q", w.messages[3].Role)
	}
}

func TestLoop_UpdateUsageAccumulates(t *testing.T) {
	prov := makeScriptedProvider("done")
	reg := emptyRegistry()
	w := &recordingWriter{}
	loop, _ := NewLoop(LoopConfig{Provider: prov, Registry: reg, Writer: w})
	events, _ := loop.Run(context.Background(), "hi")
	for range events {
	}
	if w.inTok != 3 {
		t.Errorf("inTok = %d, want 3", w.inTok)
	}
	if w.outTok != 5 {
		t.Errorf("outTok = %d, want 5", w.outTok)
	}
}
