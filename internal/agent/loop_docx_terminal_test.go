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

func TestStandaloneDocxMutationFinalReply(t *testing.T) {
	tests := []struct {
		name     string
		calls    []llm.ToolCall
		outcomes []callOutcome
		want     bool
	}{
		{
			name:     "successful create",
			calls:    []llm.ToolCall{{Name: "edit_docx", Arguments: `{"action":"create","path":"plan.docx"}`}},
			outcomes: []callOutcome{{}},
			want:     true,
		},
		{
			name:     "dry run",
			calls:    []llm.ToolCall{{Name: "edit_docx", Arguments: `{"action":"create","path":"plan.docx","dry_run":true}`}},
			outcomes: []callOutcome{{}},
		},
		{
			name:     "failed create",
			calls:    []llm.ToolCall{{Name: "edit_docx", Arguments: `{"action":"create","path":"plan.docx"}`}},
			outcomes: []callOutcome{{failed: true}},
		},
		{
			name:     "successful existing document batch",
			calls:    []llm.ToolCall{{Name: "edit_docx", Arguments: `{"action":"batch","path":"plan.docx"}`}},
			outcomes: []callOutcome{{}},
			want:     true,
		},
		{
			name:     "single legacy mutation stays open",
			calls:    []llm.ToolCall{{Name: "edit_docx", Arguments: `{"action":"replace","path":"plan.docx"}`}},
			outcomes: []callOutcome{{}},
		},
		{
			name: "mixed workflow",
			calls: []llm.ToolCall{
				{Name: "edit_docx", Arguments: `{"action":"create","path":"plan.docx"}`},
				{Name: "read_docx", Arguments: `{"path":"plan.docx"}`},
			},
			outcomes: []callOutcome{{}, {}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, got := standaloneDocxMutationFinalReply(tc.calls, tc.outcomes)
			if got != tc.want {
				t.Fatalf("terminal create = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestLoop_SuccessfulDocxCreateForcesOneToolFreeFinalReply(t *testing.T) {
	p := &stubProvider{
		name: "small-local-model",
		scripts: [][]llm.Delta{
			{
				{Role: llm.RoleAssistant},
				{ToolCall: &llm.ToolCall{ID: "create-1", Name: "edit_docx", Arguments: `{"action":"create","path":"plan.docx","text":"# Plan"}`}},
				{FinishReason: "tool_calls"},
			},
			{
				// Simulate a weak model ignoring the absence of tool schemas and
				// trying the create again. The loop must suppress it and finish.
				{Role: llm.RoleAssistant},
				{ToolCall: &llm.ToolCall{ID: "create-2", Name: "edit_docx", Arguments: `{"action":"create","path":"plan.docx","text":"changed"}`}},
				{FinishReason: "tool_calls"},
			},
		},
	}
	var executions atomic.Int32
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name:        "edit_docx",
		Description: "edit Word",
		Schema: `{"type":"object","required":["action","path"],"properties":{` +
			`"action":{"type":"string"},"path":{"type":"string"},"text":{"type":"string"}}}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			executions.Add(1)
			return tools.Result{Text: "Created plan.docx"}, nil
		},
	})
	reg.Activate("edit_docx")

	l := makeLoop(t, p, reg, "")
	ch, err := l.Run(context.Background(), "utwórz plan zajęć w Wordzie")
	if err != nil {
		t.Fatal(err)
	}
	events := drainEvents(t, ch)

	if got := executions.Load(); got != 1 {
		t.Fatalf("edit_docx executions = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&p.calls); got != 2 {
		t.Fatalf("provider calls = %d, want 2", got)
	}
	if len(p.toolReqs) != 2 || p.toolReqs[0] == 0 || p.toolReqs[1] != 0 {
		t.Fatalf("tool schema counts = %v, want [>0 0]", p.toolReqs)
	}
	if len(p.reqs) != 2 || !strings.Contains(p.reqs[1][len(p.reqs[1])-1].Content, "[final reply only]") {
		t.Fatalf("second request lacks final-only instruction: %+v", p.reqs)
	}

	var calls int
	var finalText string
	var done bool
	for _, event := range events {
		switch value := event.(type) {
		case ToolCallEvent:
			calls++
		case MessageEvent:
			finalText += value.Text
		case DoneEvent:
			done = true
		case ErrorEvent:
			t.Fatalf("unexpected loop error: %v", value.Err)
		}
	}
	if calls != 1 {
		t.Fatalf("visible tool calls = %d, want 1", calls)
	}
	if finalText != "✓ Word: plan.docx" {
		t.Fatalf("fallback reply = %q", finalText)
	}
	if !done {
		t.Fatal("missing DoneEvent")
	}
}
