package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func policyReasoning(marker string) *llm.ReasoningBlock {
	data, _ := json.Marshal(map[string]string{"reasoning_content": marker})
	return &llm.ReasoningBlock{Format: llm.ReasoningChat, Model: "fixture", Scope: "fixture", Data: data, Tokens: 1000}
}
func policyReply(marker string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Parts: []llm.ContentPart{
		{Type: llm.PartTypeReasoning, Reasoning: policyReasoning(marker)},
		{Type: llm.PartTypeText, Text: "Visible reply."},
	}}
}
func hasPolicyMarker(messages []llm.Message, marker string) bool {
	for _, m := range messages {
		for _, p := range m.Parts {
			if p.Reasoning != nil && strings.Contains(string(p.Reasoning.Data), marker) {
				return true
			}
		}
	}
	return false
}

func TestReasoningHistoryDropIsReversibleAndStableDuringTools(t *testing.T) {
	old := llm.DiscardPreviousReasoning()
	t.Cleanup(func() { llm.SetDiscardPreviousReasoning(old) })
	llm.SetDiscardPreviousReasoning(true)
	registry := tools.NewRegistry()
	registry.MustRegister(tools.Tool{Name: "fixture", Description: "Read fixture.", Schema: "{\"type\":\"object\"}", ReadOnly: true,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "Fixture evidence."}, nil
		}})
	p := &stubProvider{name: "fixture", scripts: [][]llm.Delta{
		{{NativeReasoning: policyReasoning("active-tool")}, {ToolCall: &llm.ToolCall{ID: "read", Name: "fixture", Arguments: "{}"}, FinishReason: "tool_calls"}},
		{{NativeReasoning: policyReasoning("completed-new")}, {Content: "Done.", FinishReason: "stop"}},
		{{Content: "Next reply.", FinishReason: "stop"}},
	}, onCalled: func(call int) {
		if call == 0 {
			llm.SetDiscardPreviousReasoning(false)
		}
	}}
	history := []llm.Message{{Role: llm.RoleUser, Content: "Previous task."}, policyReply("completed-old")}
	l, err := NewLoop(LoopConfig{Provider: p, Registry: registry, InitialMessages: history, MaxSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	run := func() {
		t.Helper()
		ch, err := l.Run(context.Background(), "Continue.")
		if err != nil {
			t.Fatal(err)
		}
		for ev := range ch {
			if e, ok := ev.(ErrorEvent); ok {
				t.Fatal(e.Err)
			}
		}
	}
	run()
	if len(p.reqs) != 2 {
		t.Fatalf("requests=%d", len(p.reqs))
	}
	for _, req := range p.reqs {
		if hasPolicyMarker(req, "completed-old") {
			t.Fatal("old reply reasoning replayed in drop mode")
		}
	}
	if !hasPolicyMarker(p.reqs[1], "active-tool") {
		t.Fatal("active tool reasoning removed")
	}
	if !hasPolicyMarker(l.Messages, "completed-old") {
		t.Fatal("archive/in-memory history modified")
	}
	run()
	if len(p.reqs) != 3 || !hasPolicyMarker(p.reqs[2], "completed-old") || !hasPolicyMarker(p.reqs[2], "completed-new") {
		t.Fatal("keep did not restore historical reasoning on the next turn")
	}
}

func TestReasoningHistoryDropPreservesProtocolAndIgnoresImageTurns(t *testing.T) {
	tool := policyReply("required-tool")
	tool.ToolCalls = []llm.ToolCall{{ID: "read", Name: "fixture", Arguments: "{}"}}
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "Old task."}, tool,
		{Role: llm.RoleTool, ToolCallID: "read", Content: "Evidence."},
		policyReply("old-final"),
		{Role: llm.RoleUser, Content: "Current task."},
		policyReply("active-reasoning"),
		{Role: llm.RoleUser, Content: "Attached image from tool read_image:"},
		{Role: llm.RoleUser, Content: "<task-notification>worker update</task-notification>"},
	}
	before, _ := json.Marshal(messages)
	l := &Loop{discardPreviousReasoning: true}
	view := l.reasoningHistoryView(messages)
	if hasPolicyMarker(view, "old-final") || !hasPolicyMarker(view, "required-tool") || !hasPolicyMarker(view, "active-reasoning") {
		t.Fatal("wrong completed-turn boundary or tool state removed")
	}
	if llm.EstimateTokens(messages)-llm.EstimateTokens(view) != 1000 {
		t.Fatal("discarded reasoning still budgeted")
	}
	after, _ := json.Marshal(messages)
	if string(before) != string(after) {
		t.Fatal("source history changed")
	}
	// A resumed unfinished tool exchange has no final reply boundary.
	unfinished := append(append([]llm.Message(nil), messages[:3]...), llm.Message{Role: llm.RoleUser, Content: "Continue the interrupted work."})
	if !hasPolicyMarker(l.reasoningHistoryView(unfinished), "required-tool") {
		t.Fatal("unfinished exchange lost reasoning")
	}
}
