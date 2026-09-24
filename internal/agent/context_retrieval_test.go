package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestWorkerContinuationRetainsEvidenceWithoutOwnSessionStore(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprintf("thin=%v", thin), func(t *testing.T) {
			const evidence = "specific finding: parser.go:42 uses a shared mutable request buffer"
			provider := &stubProvider{name: "worker", scripts: [][]llm.Delta{
				{{ToolCall: &llm.ToolCall{ID: "read", Name: "read_lines", Arguments: `{"file":"parser.go"}`}}},
				{{Content: "Investigation complete.", FinishReason: "stop"}},
				{{Content: "Continuing from the finding.", FinishReason: "stop"}},
			}}
			base := tools.NewRegistry()
			reads := 0
			base.MustRegister(tools.Tool{Name: "read_lines", Description: "read", ReadOnly: true, Schema: `{"type":"object"}`,
				Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
					reads++
					return tools.Result{Text: evidence}, nil
				},
			})
			base.MarkAlwaysOn("read_lines")
			// Production general workers inherit this closure, but their own tool
			// transcript is not written to the parent's session store.
			base.MustRegister(tools.Tool{Name: "search_history", Description: "search parent history", ReadOnly: true, Schema: `{"type":"object"}`,
				Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
					t.Error("continuation attempted to rediscover its own result")
					return tools.Result{Text: "no matches"}, nil
				},
			})
			parent := makeLoop(t, &stubProvider{}, base, "")
			parent.thinTools = thin
			specs := NewSubAgentRegistry()
			MustRegisterAll(specs, BuiltinSubAgents())
			task, err := NewAgentTool(specs, parent, base, provider, nil, NewLoop)
			if err != nil {
				t.Fatal(err)
			}
			result, err := task.execute(context.Background(), json.RawMessage(`{"prompt":"Investigate the parser."}`))
			if err != nil || result.Err != nil {
				t.Fatalf("task: %v %+v", err, result)
			}
			worker, ok := task.Workers.Get("worker-1")
			if !ok {
				t.Fatal("missing worker")
			}
			if worker.Loop.writer != nil {
				t.Fatal("fixture unexpectedly has a worker transcript store")
			}
			before := append([]llm.Message(nil), provider.reqs[1]...)
			// Request-time tail blocks (clock and optional unhoisted catalog)
			// move after new messages; the existing conversation must not.
			for len(before) > 0 && before[len(before)-1].Role == llm.RoleSystem {
				before = before[:len(before)-1]
			}
			followup := NewSendMessageTool(task.Workers)
			result, err = followup.execute(context.Background(), json.RawMessage(`{"to":"worker-1","message":"Continue using the finding."}`))
			if err != nil || result.Err != nil {
				t.Fatalf("continuation: %v %+v", err, result)
			}
			if reads != 1 || provider.calls != 3 {
				t.Fatalf("reads=%d requests=%d", reads, provider.calls)
			}
			resumed := provider.reqs[2]
			found := false
			for _, m := range resumed {
				if m.Role == llm.RoleTool && strings.Contains(m.Content, evidence) {
					found = true
				}
			}
			if !found {
				t.Fatal("worker's completed tool evidence vanished from its continuation")
			}
			if len(resumed) < len(before) || !reflect.DeepEqual(before, resumed[:len(before)]) {
				t.Fatal("continuation rewrote the existing conversation prefix")
			}
		})
	}
}

func TestResolvedHistoryRequiresAvailableTranscript(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "inspect"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "read", Name: "read_lines", Arguments: "{}"}}},
		{Role: llm.RoleTool, ToolCallID: "read", Name: "read_lines", Content: strings.Repeat("critical evidence ", 500)},
		{Role: llm.RoleAssistant, Content: "finished"},
		{Role: llm.RoleUser, Content: "continue"},
	}
	for _, kind := range []string{"no writer", "no history tool", "outage", "pending", "lost messages", "healthy", "usage failure only"} {
		t.Run(kind, func(t *testing.T) {
			reg := tools.NewRegistry()
			if kind != "no history tool" {
				reg.MustRegister(tools.Tool{Name: "search_history", Description: "history", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }})
			}
			l := &Loop{registry: reg, Messages: messages}
			if kind != "no writer" {
				l.writer = &recordingWriter{}
			}
			switch kind {
			case "outage":
				l.persistHealth.outage = true
			case "pending":
				l.persistHealth.pending = []llm.Message{messages[2]}
			case "lost messages":
				l.persistHealth.dropped = 1
			case "usage failure only":
				l.persistHealth.failures = 1
				l.persistHealth.firstOp = "update_usage"
			}
			wantKeep := kind != "healthy" && kind != "usage failure only"
			got := l.resolvedToolProviderView(messages)
			if requestContainsToolProtocol(got) != wantKeep {
				t.Fatalf("evidence retained=%v, want %v", requestContainsToolProtocol(got), wantKeep)
			}
			if messages[2].Content != strings.Repeat("critical evidence ", 500) {
				t.Fatal("canonical evidence mutated")
			}
		})
	}
}
