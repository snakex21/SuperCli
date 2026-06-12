package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// TestLoop_ChatRouteSendsMinimalToolSet: the chat route must send only
// tool_search + recall (when registered), never the full tool list.
func TestLoop_ChatRouteSendsMinimalToolSet(t *testing.T) {
	p := &captureProvider{name: "capture"}
	reg := tools.NewRegistry()
	noop := func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
		return tools.Result{Text: "x"}, nil
	}
	for _, name := range []string{"tool_search", "recall", "expensive_tool", "another_big_tool"} {
		reg.MustRegister(tools.Tool{Name: name, Description: "d " + name, Schema: `{"type":"object"}`, Fn: noop})
		reg.MarkAlwaysOn(name)
	}
	l := makeLoop(t, p, reg, "FULL COORDINATOR PROMPT")
	l.navigate = true
	ch, _ := l.Run(context.Background(), "cześć")
	drainEvents(t, ch)

	if p.toolCount != 2 {
		t.Fatalf("toolCount=%d, want 2 (tool_search + recall)", p.toolCount)
	}
}

// TestLoop_CoordinatorRouteAppendsTimeStamp: every coordinator request
// ends with a fresh system message carrying date/time/zone.
func TestLoop_CoordinatorRouteAppendsTimeStamp(t *testing.T) {
	reg := tools.NewRegistry()
	l, err := NewLoop(LoopConfig{
		Provider: &stubProvider{name: "stub"},
		Registry: reg,
		System:   "base system",
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	l.route = RouteCoordinator
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleUser, Content: "zrób coś"})
	msgs := l.providerMessages()
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleSystem || !strings.Contains(last.Content, "Current local date/time:") {
		t.Fatalf("last message = %+v, want trailing time-stamp system message", last)
	}
	if !strings.Contains(last.Content, "2026") {
		t.Errorf("time stamp missing year sentence: %q", last.Content)
	}
}

// TestLoop_ChatRouteKeepsCurrentTurnToolPairs: tool calls made during
// the current chat-route turn must survive providerMessages so the
// provider sees valid call/result pairing.
func TestLoop_ChatRouteKeepsCurrentTurnToolPairs(t *testing.T) {
	reg := tools.NewRegistry()
	l, err := NewLoop(LoopConfig{Provider: &stubProvider{name: "stub"}, Registry: reg, System: "base"})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	l.route = RouteChatOnly
	l.Messages = append(l.Messages,
		llm.Message{Role: llm.RoleUser, Content: "stare pytanie"},
		llm.Message{Role: llm.RoleAssistant, Content: "stara odpowiedź"},
		llm.Message{Role: llm.RoleUser, Content: "co słychać w go 1.25?"},
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "tool_search", Arguments: `{"query":"web"}`}}},
		llm.Message{Role: llm.RoleTool, ToolCallID: "c1", Name: "tool_search", Content: "found web_search"},
	)
	msgs := l.providerMessages()
	var sawCall, sawResult bool
	for _, m := range msgs {
		if len(m.ToolCalls) > 0 {
			sawCall = true
		}
		if m.Role == llm.RoleTool {
			sawResult = true
		}
	}
	if !sawCall || !sawResult {
		t.Fatalf("tool pairing lost: call=%v result=%v msgs=%+v", sawCall, sawResult, msgs)
	}
}
