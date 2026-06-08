package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// scriptedToolCallProvider returns a tool_call on its first
// call and a final text on subsequent calls. The "sub-agent
// aware" branch detects the child's system prompt and emits
// "child-ok" so the test can confirm the child ran.
type scriptedToolCallProvider struct {
	name     string
	toolName string // empty on child provider = text-only
	toolArgs string
	final    string
	toolID   string
	calls    int32
}

func (p *scriptedToolCallProvider) Name() string { return p.name }
func (p *scriptedToolCallProvider) SupportsVision() bool {
	return false
}

func (p *scriptedToolCallProvider) Complete(ctx context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	idx := int(atomic.AddInt32(&p.calls, 1)) - 1
	// Detect the child's system prompt. The AgentTool sets
	// the sub-agent's System on the child loop, so its model
	// call carries that string.
	system := ""
	for _, m := range msgs {
		if m.Role == llm.RoleSystem {
			system = m.Content
			break
		}
	}
	ch := make(chan llm.Delta, 4)
	go func() {
		defer close(ch)
		// First call: emit a tool_call for the task tool.
		// Skip this branch on the child (toolName == "").
		if idx == 0 && p.toolName != "" {
			select {
			case <-ctx.Done():
				return
			case ch <- llm.Delta{
				Role: llm.RoleAssistant,
				ToolCall: &llm.ToolCall{
					ID:        p.toolID,
					Name:      p.toolName,
					Arguments: p.toolArgs,
				},
			}:
			}
			select {
			case <-ctx.Done():
				return
			case ch <- llm.Delta{FinishReason: "tool_calls"}:
			}
			return
		}
		// Final text. Use "child-ok" when this is the child's
		// call so the parent's tool result text contains the
		// signal.
		body := p.final
		if strings.Contains(system, "sub-agent") || strings.Contains(system, "exploration") {
			body = "child-ok"
		}
		select {
		case <-ctx.Done():
			return
		case ch <- llm.Delta{Role: llm.RoleAssistant, Content: body}:
		}
		select {
		case <-ctx.Done():
			return
		case ch <- llm.Delta{FinishReason: "stop", Usage: &llm.Usage{Input: 7, Output: 4, Total: 11}}:
		}
	}()
	return ch, nil
}

// TestAgentTool_Integration_ParentCallsTaskAndContinues
// drives the parent loop through a real tool call to the
// AgentTool. The parent provider emits a tool_call for
// "task" on its first turn, then a final text on its second
// turn after seeing the tool result.
func TestAgentTool_Integration_ParentCallsTaskAndContinues(t *testing.T) {
	// Parent provider: 1st call -> tool_call(task),
	// 2nd call -> final text reading the tool result.
	parentProv := &scriptedToolCallProvider{
		name:     "parent",
		toolName: "task",
		toolArgs: `{"agent":"explore","prompt":"find X"}`,
		final:    "found it",
		toolID:   "call_task_1",
	}
	// Child provider: text-only, returns "child-ok".
	childProv := &scriptedToolCallProvider{
		name:  "child",
		final: "child-ok",
	}

	parentLoop := &Loop{system: "parent voice", Messages: nil}
	base := tools.NewRegistry()
	subReg := NewSubAgentRegistry()
	MustRegisterAll(subReg, []SubAgent{
		{
			Name:        "explore",
			Description: "exploration sub-agent",
			System:      "You are a sub-agent specialized in exploration.",
			MaxSteps:    4,
		},
	})
	at, err := NewAgentTool(
		subReg, parentLoop, base, childProv, nil,
		func(cfg LoopConfig) (*Loop, error) { return NewLoop(cfg) },
	)
	if err != nil {
		t.Fatalf("NewAgentTool: %v", err)
	}
	if err := base.Register(at.Spec()); err != nil {
		t.Fatalf("register task: %v", err)
	}

	loop, err := NewLoop(LoopConfig{
		Provider: parentProv,
		Registry: base,
		System:   "parent voice",
		MaxSteps: 5,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	parentLoop = loop

	events, err := loop.Run(context.Background(), "delegate to explore")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	evs := drainEvents(t, events)

	// Expected: parent emits a tool_call(task), the loop
	// dispatches the tool (returning "child-ok"), the parent
	// provider's second call emits a final text containing
	// "found it". Verify by walking the events.
	var sawToolCall, sawToolResult, sawFinal bool
	for _, ev := range evs {
		switch e := ev.(type) {
		case MessageEvent:
			if strings.Contains(e.Text, "found it") {
				sawFinal = true
			}
		case ToolCallEvent:
			if e.Name == "task" || e.ID == parentProv.toolID {
				sawToolCall = true
			}
		case ToolResultEvent:
			if e.ID == parentProv.toolID {
				sawToolResult = true
			}
		}
	}
	if !sawToolCall {
		t.Errorf("expected tool call event for task; got events: %+v", evs)
	}
	if !sawToolResult {
		t.Errorf("expected tool result event for task")
	}
	if !sawFinal {
		t.Errorf("expected final assistant text 'found it'")
	}
}

// TestAgentTool_Integration_ChildLoopIsIndependent makes
// sure the parent's messages do not leak into the child
// loop's seed when share_context is false (the default).
func TestAgentTool_Integration_ChildLoopIsIndependent(t *testing.T) {
	parentLoop := &Loop{system: "parent", Messages: nil}
	base := tools.NewRegistry()
	subReg := NewSubAgentRegistry()
	MustRegisterAll(subReg, []SubAgent{
		{Name: "explore", Description: "x", System: "child sys", MaxSteps: 4},
	})
	prov := &scriptedToolCallProvider{
		name:  "child",
		final: "child-ok",
	}
	at, err := NewAgentTool(
		subReg, parentLoop, base, prov, nil,
		func(cfg LoopConfig) (*Loop, error) { return NewLoop(cfg) },
	)
	if err != nil {
		t.Fatalf("NewAgentTool: %v", err)
	}
	// Override the factory so we can capture the child's
	// InitialMessages and use a one-step child provider.
	var capturedSeed []llm.Message
	at.NewLoop = func(cfg LoopConfig) (*Loop, error) {
		capturedSeed = cfg.InitialMessages
		return NewLoop(LoopConfig{
			Provider: &scriptedToolCallProvider{name: "c", final: "child-ok"},
			Registry: tools.NewRegistry(),
			System:   cfg.System,
			MaxSteps: 1,
		})
	}
	_, err = at.execute(context.Background(), json.RawMessage(`{"agent":"explore","prompt":"hi"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// share_context=false is the default; seed must be
	// empty.
	if len(capturedSeed) != 0 {
		t.Errorf("expected empty seed with share_context=false, got %d msgs", len(capturedSeed))
	}
}
