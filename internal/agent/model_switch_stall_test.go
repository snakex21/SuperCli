package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// Reported: after switching from an Anthropic model to a Responses model the
// turn stopped mid-task with no answer. The Responses backend reported a few
// percent fewer input tokens for a larger request (52277 -> 50904 in a 500k
// window); that was read as server-side truncation, forced a compaction of an
// already summarized prefix and its "insufficient reduction" error ended the
// turn. Far below the window a shrinking count is jitter, not truncation.
func TestHostedInputJitterFarBelowWindowIsNotTruncation(t *testing.T) {
	echo, _ := llm.NewEcho("gpt-6.2-sol")
	l := &Loop{provider: echo, route: RouteCoordinator,
		contextWindowFor: func(string) ContextWindowResolution {
			return ContextWindowResolution{Tokens: 500_000, Source: "catalog"}
		}}
	l.Messages = []llm.Message{{Role: llm.RoleUser, Content: strings.Repeat("base ", 300)}}
	if l.recordContextBaseline(l.estimateNextRequestTokensRaw(), 52_277) {
		t.Fatal("first baseline reported as truncation")
	}
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleTool, Content: strings.Repeat("new-result ", 60)})
	if l.recordContextBaseline(l.estimateNextRequestTokensRaw(), 50_904) {
		t.Fatal("a 3% drop at 10% of a 500k window was treated as server truncation")
	}
	if l.takeServerTruncated() {
		t.Fatal("jitter requested a context-limit compaction")
	}
}

// Truncation evidence alone (no provider rejection) must not fail the turn
// when the summary cannot shrink the history and the request still fits.
func TestServerTruncatedCompactionThatCannotShrinkKeepsTheTurn(t *testing.T) {
	echo, _ := llm.NewEcho("gpt-6.2-sol")
	l := &Loop{provider: echo, route: RouteCoordinator,
		contextWindowFor: func(string) ContextWindowResolution {
			return ContextWindowResolution{Tokens: 500_000, Source: "catalog"}
		}}
	l.Messages = []llm.Message{
		{Role: llm.RoleUser, Content: "first task"},
		{Role: llm.RoleAssistant, Content: "done"},
		{Role: llm.RoleUser, Content: "second task"},
		{Role: llm.RoleAssistant, Content: "done too"},
		{Role: llm.RoleUser, Content: "continue"},
	}
	// A summary larger than what it replaces: "insufficient reduction".
	l.summarizer = func(_ context.Context, _ llm.Provider, msgs []llm.Message) (string, error) {
		return strings.Repeat("summary of everything so far ", 200), nil
	}
	if err := l.maybeAutoCompact(context.Background(), nil, serverTruncatedReason); err != nil {
		t.Fatalf("truncation evidence under the window aborted the turn: %v", err)
	}
	if err := l.maybeAutoCompact(context.Background(), nil, "context-limit"); err == nil || !strings.Contains(err.Error(), "insufficient reduction") {
		t.Fatalf("a real provider context-limit rejection must still fail loudly, got %v", err)
	}
}

func echoRegistry() *tools.Registry {
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name: "echo", Description: "echo",
		Schema: `{"type":"object","properties":{"msg":{"type":"string"}}}`,
		Fn: func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "echoed"}, nil
		},
	})
	return reg
}

func runDone(t *testing.T, l *Loop, prompt string) []Event {
	t.Helper()
	ch, err := l.Run(context.Background(), prompt)
	if err != nil {
		t.Fatal(err)
	}
	events := drainEvents(t, ch)
	for _, ev := range events {
		if e, ok := ev.(ErrorEvent); ok {
			t.Fatalf("turn failed: %v", e.Err)
		}
	}
	if _, ok := events[len(events)-1].(DoneEvent); !ok {
		t.Fatalf("turn did not finish with Done: %T", events[len(events)-1])
	}
	return events
}

// Switching providers mid-conversation keeps tool calls, results and
// reasoning paired, both ways, and the new model's tool use completes.
func TestProviderSwitchMidConversationWithToolsAndReasoning(t *testing.T) {
	toolStep := func(id, reasoning string) []llm.Delta {
		return []llm.Delta{
			{Role: llm.RoleAssistant, Reasoning: reasoning},
			{ToolCall: &llm.ToolCall{ID: id, Name: "echo", Arguments: `{"msg":"hi"}`}},
			{FinishReason: "tool_calls", Usage: &llm.Usage{Input: 10, Output: 2, Total: 12}},
		}
	}
	final := func(text string) []llm.Delta {
		return []llm.Delta{{Role: llm.RoleAssistant, Content: text}, {FinishReason: "stop", Usage: &llm.Usage{Input: 12, Output: 2, Total: 14}}}
	}
	opus := &stubProvider{name: "claude-opus-5-5", scripts: [][]llm.Delta{toolStep("toolu_01", "thinking about it"), final("first done")}}
	gpt := &stubProvider{name: "gpt-6.2-sol", scripts: [][]llm.Delta{toolStep("call_ab", "plan"), final("second done")}}
	l := makeLoop(t, opus, echoRegistry(), "")
	runDone(t, l, "task one")
	l.SetModel(gpt)
	runDone(t, l, "task two")
	l.SetModel(opus)
	opus.scripts = append(opus.scripts, final("third done"))
	runDone(t, l, "task three")

	for name, reqs := range map[string][][]llm.Message{"gpt": gpt.reqs, "opus": opus.reqs} {
		for _, msgs := range reqs {
			open := map[string]bool{}
			for _, m := range msgs {
				for _, c := range m.ToolCalls {
					open[c.ID] = true
				}
				if m.Role == llm.RoleTool {
					if !open[m.ToolCallID] {
						t.Fatalf("%s request: tool result %q without its call", name, m.ToolCallID)
					}
					delete(open, m.ToolCallID)
				}
			}
			if len(open) > 0 {
				t.Fatalf("%s request: tool calls without results: %v", name, open)
			}
		}
	}
	// The new model saw the earlier tool exchange.
	last := gpt.reqs[0]
	seen := false
	for _, m := range last {
		for _, c := range m.ToolCalls {
			seen = seen || c.ID == "toolu_01"
		}
	}
	if !seen {
		t.Fatal("the switched-to model lost the earlier tool call from history")
	}
}

// A reply cut by the output limit (finish_reason length / Responses
// "incomplete") is continued automatically instead of ending the turn.
func TestCutOffReplyContinuesAutomatically(t *testing.T) {
	p := &stubProvider{name: "gpt-6.2-sol", scripts: [][]llm.Delta{
		{{Role: llm.RoleAssistant, Content: "Here is the first half"}, {FinishReason: "length", Usage: &llm.Usage{Input: 5, Output: 100, Total: 105}}},
		{{Role: llm.RoleAssistant, Content: " and the rest."}, {FinishReason: "stop", Usage: &llm.Usage{Input: 6, Output: 4, Total: 10}}},
	}}
	l := makeLoop(t, p, echoRegistry(), "")
	events := runDone(t, l, "write it")
	if p.calls != 2 {
		t.Fatalf("calls = %d, want 2 (one automatic continuation)", p.calls)
	}
	noticed := false
	for _, ev := range events {
		if n, ok := ev.(NoticeEvent); ok && strings.HasPrefix(n.Text, "reply cut off by the output limit") {
			noticed = true
		}
	}
	if !noticed {
		t.Fatal("no notice for the automatic continuation")
	}
	second := p.reqs[1]
	if m := second[len(second)-1]; m.Role != llm.RoleSystem || !strings.Contains(m.Content, "cut off by the output limit") {
		t.Fatalf("the continuation request does not ask to continue: %+v", m)
	}
}

// A cut-off reply that keeps hitting the limit ends with a visible note.
func TestRepeatedCutOffEndsWithIncompleteNote(t *testing.T) {
	p := &stubProvider{name: "gpt-6.2-sol", scripts: [][]llm.Delta{
		{{Role: llm.RoleAssistant, Content: "more text"}, {FinishReason: "length", Usage: &llm.Usage{Input: 5, Output: 100, Total: 105}}},
	}}
	l := makeLoop(t, p, echoRegistry(), "")
	events := runDone(t, l, "write it")
	if p.calls != 1+maxCutOffContinues {
		t.Fatalf("calls = %d, want %d", p.calls, 1+maxCutOffContinues)
	}
	noted := false
	for _, ev := range events {
		if n, ok := ev.(AnswerNoteEvent); ok && n.Kind == llm.NoteCutOff {
			noted = true
		}
	}
	if !noted {
		t.Fatal("an answer still cut off after the continuations has no note")
	}
}

// A reply with only reasoning (no text, no tools) gets one more request.
func TestReasoningOnlyReplyAsksToContinue(t *testing.T) {
	p := &stubProvider{name: "gpt-6.2-sol", scripts: [][]llm.Delta{
		{{Role: llm.RoleAssistant, Reasoning: "I should look at the files"}, {FinishReason: "stop", Usage: &llm.Usage{Input: 5, Output: 10, Total: 15}}},
		{{Role: llm.RoleAssistant, Content: "Here is the answer."}, {FinishReason: "stop", Usage: &llm.Usage{Input: 6, Output: 4, Total: 10}}},
	}}
	l := makeLoop(t, p, echoRegistry(), "")
	events := runDone(t, l, "what now")
	if p.calls != 2 {
		t.Fatalf("calls = %d, want 2", p.calls)
	}
	answered := false
	for _, ev := range events {
		if m, ok := ev.(MessageEvent); ok && strings.Contains(m.Text, "Here is the answer.") {
			answered = true
		}
	}
	if !answered {
		t.Fatal("the continued answer was not streamed")
	}
}
