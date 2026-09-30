package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestStreamTruncationRejectsEntireToolBatchAndKeepsUsage(t *testing.T) {
	for _, reason := range []string{"length", "max_tokens"} {
		t.Run(reason, func(t *testing.T) {
			executed := 0
			reg := tools.NewRegistry()
			reg.MustRegister(tools.Tool{Name: "mutate", Description: "test mutation", Schema: "{\"type\":\"object\"}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				executed++
				return tools.Result{Text: "unexpected mutation"}, nil
			}})
			p := &stubProvider{name: "fixture", scripts: [][]llm.Delta{
				{
					{ToolCall: &llm.ToolCall{ID: "complete", Name: "mutate", Arguments: "{\"content\":\"valid prefix\"}"}},
					{ToolCall: &llm.ToolCall{ID: "partial", Name: "mutate", Arguments: "{\"content\":\"cut off"}},
					{FinishReason: reason},
					// OpenAI-compatible servers can send accounting after the terminal frame.
					{Usage: &llm.Usage{Input: 10, Output: 4, Total: 14, CachedInput: 3, Reasoning: 2}},
					{FinishReason: "stop"},
				},
				{{Content: "Could not execute the incomplete request."}, {FinishReason: "stop", Usage: &llm.Usage{Input: 2, Output: 1, Total: 3}}},
			}}
			writer := &recordingWriter{}
			loop, err := NewLoop(LoopConfig{Provider: p, Registry: reg, Writer: writer})
			if err != nil {
				t.Fatal(err)
			}
			events := drainEvents(t, mustRun(t, loop, "write the result"))
			if executed != 0 || p.calls != 2 {
				t.Fatalf("executed=%d provider calls=%d", executed, p.calls)
			}
			var failedIDs []string
			var done *DoneEvent
			for _, event := range events {
				switch e := event.(type) {
				case ToolResultEvent:
					if e.Err == nil || !strings.Contains(e.Err.Error(), "output token limit") {
						t.Fatalf("tool result=%+v", e)
					}
					failedIDs = append(failedIDs, e.ID)
				case DoneEvent:
					done = &e
				case ErrorEvent:
					t.Fatalf("first truncation should allow correction: %v", e.Err)
				}
			}
			if strings.Join(failedIDs, ",") != "complete,partial" {
				t.Fatalf("result IDs=%v", failedIDs)
			}
			if done == nil || done.Usage != (Usage{Input: 12, Output: 5, Total: 17, Cached: 3, Reasoning: 2}) {
				t.Fatalf("done=%+v", done)
			}
			if writer.inTok != 12 || writer.outTok != 5 {
				t.Fatalf("persisted usage=%d/%d", writer.inTok, writer.outTok)
			}
			assertTruncationPairs(t, writer.messages, 2)
			assertTruncationPairs(t, p.reqs[1], 2)
		})
	}
}

func TestStreamTruncationRejectsTextProtocolCalls(t *testing.T) {
	for _, content := range []string{
		"<tool_call><function=mutate><parameter=content>valid prefix</parameter></function></tool_call>",
		"«mutate\ncontent: valid prefix»",
	} {
		t.Run(content[:11], func(t *testing.T) {
			executed := 0
			reg := tools.NewRegistry()
			reg.MustRegister(tools.Tool{Name: "mutate", Description: "test mutation", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				executed++
				return tools.Result{Text: "unexpected mutation"}, nil
			}})
			p := &stubProvider{name: "fixture", scripts: [][]llm.Delta{
				{{Content: content}, {FinishReason: "length"}},
				{{Content: "The response was incomplete."}, {FinishReason: "stop"}},
			}}
			loop := makeLoop(t, p, reg, "")
			drainEvents(t, mustRun(t, loop, "write the result"))
			if executed != 0 || p.calls != 2 {
				t.Fatalf("executed=%d provider calls=%d", executed, p.calls)
			}
			assertTruncationPairs(t, p.reqs[1], 1)
		})
	}
}

func TestStreamTruncationAllowsCompleteRetryAndNormalFinishes(t *testing.T) {
	for _, reason := range []string{"tool_calls", "stop"} {
		t.Run(reason, func(t *testing.T) {
			var executed []string
			reg := tools.NewRegistry()
			reg.MustRegister(tools.Tool{Name: "mutate", Description: "test mutation", Schema: "{\"type\":\"object\"}", Fn: func(_ context.Context, raw json.RawMessage) (tools.Result, error) {
				executed = append(executed, string(raw))
				return tools.Result{Text: "complete mutation"}, nil
			}})
			p := &stubProvider{name: "fixture", scripts: [][]llm.Delta{
				{{ToolCall: &llm.ToolCall{ID: "partial", Name: "mutate", Arguments: "{\"content\":\"prefix\"}"}}, {FinishReason: "length"}},
				{{ToolCall: &llm.ToolCall{ID: "corrected", Name: "mutate", Arguments: "{\"content\":\"complete\"}"}}, {FinishReason: reason}},
				{{Content: "Completed."}, {FinishReason: "stop"}},
			}}
			loop := makeLoop(t, p, reg, "")
			events := drainEvents(t, mustRun(t, loop, "write the result"))
			if len(executed) != 1 || !strings.Contains(executed[0], "complete") || p.calls != 3 {
				t.Fatalf("executed=%v provider calls=%d", executed, p.calls)
			}
			for _, event := range events {
				if e, ok := event.(ErrorEvent); ok {
					t.Fatal(e.Err)
				}
			}
		})
	}
}

func TestStreamTruncationRetriesAreBoundedByResponses(t *testing.T) {
	executed := 0
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "mutate", Description: "test mutation", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		executed++
		return tools.Result{Text: "unexpected mutation"}, nil
	}})
	scripts := make([][]llm.Delta, maxToolFormatRetries+1)
	for i := range scripts {
		// Changing arguments/IDs must not defeat the bound, and two calls in one
		// response must not consume two independent correction opportunities.
		for j := 0; j < 2; j++ {
			scripts[i] = append(scripts[i], llm.Delta{ToolCall: &llm.ToolCall{ID: fmt.Sprintf("%d-%d", i, j), Name: "mutate", Arguments: fmt.Sprintf("{\"content\":\"partial %d-%d\"}", i, j)}})
		}
		scripts[i] = append(scripts[i], llm.Delta{FinishReason: "length", Usage: &llm.Usage{Input: 10, Output: 4, Total: 14}})
	}
	p := &stubProvider{name: "fixture", scripts: scripts}
	writer := &recordingWriter{}
	loop, err := NewLoop(LoopConfig{Provider: p, Registry: reg, Writer: writer, MaxSteps: 50})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a fresh provider projection after compaction. Retry accounting
	// must survive losing every prior error envelope from the in-memory view.
	p.onCalled = func(call int) {
		if call > 0 {
			loop.Messages = []llm.Message{{Role: llm.RoleUser, Content: "compacted context"}}
		}
	}
	for run := 1; run <= 2; run++ {
		events := drainEvents(t, mustRun(t, loop, "try writing the complete result"))
		terminal, ok := events[len(events)-1].(ErrorEvent)
		if !ok || terminal.Err == nil || !strings.Contains(terminal.Err.Error(), "output token limit") {
			t.Fatalf("terminal=%+v", events[len(events)-1])
		}
		attempts := maxToolFormatRetries + 1
		if executed != 0 || int(p.calls) != attempts*run || terminal.Steps != attempts || terminal.Usage.Total != 14*attempts {
			t.Fatalf("executed=%d provider calls=%d terminal=%+v", executed, p.calls, terminal)
		}
	}
	assertTruncationPairs(t, writer.messages, 4*(maxToolFormatRetries+1))
}

func TestStreamTruncationBoundResetsAfterCompleteResponse(t *testing.T) {
	executed := 0
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "mutate", Description: "test mutation", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		executed++
		return tools.Result{Text: "complete mutation"}, nil
	}})
	var scripts [][]llm.Delta
	for segment := 0; segment < 2; segment++ {
		for attempt := 0; attempt < maxToolFormatRetries; attempt++ {
			scripts = append(scripts, []llm.Delta{
				{ToolCall: &llm.ToolCall{ID: fmt.Sprintf("partial-%d-%d", segment, attempt), Name: "mutate", Arguments: "{}"}},
				{FinishReason: "length"},
			})
		}
		scripts = append(scripts, []llm.Delta{
			{ToolCall: &llm.ToolCall{ID: fmt.Sprintf("complete-%d", segment), Name: "mutate", Arguments: "{}"}},
			{FinishReason: "tool_calls"},
		})
	}
	scripts = append(scripts, []llm.Delta{{Content: "Completed."}, {FinishReason: "stop"}})
	p := &stubProvider{name: "fixture", scripts: scripts}
	loop, err := NewLoop(LoopConfig{Provider: p, Registry: reg, MaxSteps: 20})
	if err != nil {
		t.Fatal(err)
	}
	events := drainEvents(t, mustRun(t, loop, "write both parts"))
	if _, ok := events[len(events)-1].(DoneEvent); !ok || executed != 2 || int(p.calls) != len(scripts) {
		t.Fatalf("executed=%d provider calls=%d terminal=%+v", executed, p.calls, events[len(events)-1])
	}
}

func assertTruncationPairs(t *testing.T, messages []llm.Message, want int) {
	t.Helper()
	pending := map[string]bool{}
	paired := 0
	for _, message := range messages {
		switch message.Role {
		case llm.RoleAssistant:
			if len(pending) != 0 {
				t.Fatalf("assistant arrived before tool results: %v", pending)
			}
			for _, call := range message.ToolCalls {
				if call.ID == "" || !json.Valid([]byte(call.Arguments)) {
					t.Fatalf("call cannot be replayed: %+v", call)
				}
				pending[call.ID] = true
			}
		case llm.RoleTool:
			if !pending[message.ToolCallID] || !strings.Contains(message.Content, "output token limit") {
				t.Fatalf("unpaired or successful tool result: %+v", message)
			}
			delete(pending, message.ToolCallID)
			paired++
		}
	}
	if len(pending) != 0 || paired != want {
		t.Fatalf("pairs=%d want=%d pending=%v", paired, want, pending)
	}
}
