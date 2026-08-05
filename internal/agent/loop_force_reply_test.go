package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// toolCountProvider records how many tool defs each Complete receives.
type toolCountProvider struct {
	mu         sync.Mutex
	toolCounts []int
	scripts    [][]llm.Delta
	calls      int
}

func (p *toolCountProvider) Name() string         { return "toolcount" }
func (p *toolCountProvider) SupportsVision() bool { return false }

func (p *toolCountProvider) Complete(_ context.Context, _ []llm.Message, tools []llm.ToolDef) (<-chan llm.Delta, error) {
	p.mu.Lock()
	p.toolCounts = append(p.toolCounts, len(tools))
	idx := p.calls
	if idx >= len(p.scripts) {
		idx = len(p.scripts) - 1
	}
	p.calls++
	script := p.scripts[idx]
	p.mu.Unlock()

	ch := make(chan llm.Delta, len(script)+1)
	go func() {
		defer close(ch)
		for _, d := range script {
			ch <- d
		}
	}()
	return ch, nil
}

func TestLoop_ForceReplySendsZeroTools(t *testing.T) {
	// Build a discovery loop that hits hard force: period-2 cycle A-B-A-B
	// on read_lines with alternating files, then a text-only final turn.
	// Force-reply turn must Complete with 0 tool defs.
	p := &toolCountProvider{
		scripts: [][]llm.Delta{
			{
				{Role: llm.RoleAssistant},
				{ToolCall: &llm.ToolCall{ID: "1", Name: "read_lines", Arguments: `{"file":"a"}`}},
				{FinishReason: "tool_calls"},
			},
			{
				{Role: llm.RoleAssistant},
				{ToolCall: &llm.ToolCall{ID: "2", Name: "read_lines", Arguments: `{"file":"b"}`}},
				{FinishReason: "tool_calls"},
			},
			{
				{Role: llm.RoleAssistant},
				{ToolCall: &llm.ToolCall{ID: "3", Name: "read_lines", Arguments: `{"file":"a"}`}},
				{FinishReason: "tool_calls"},
			},
			{
				{Role: llm.RoleAssistant},
				{ToolCall: &llm.ToolCall{ID: "4", Name: "read_lines", Arguments: `{"file":"b"}`}},
				{FinishReason: "tool_calls"},
			},
			// Force-reply turn (tools should be nil)
			{
				{Role: llm.RoleAssistant, Content: "Zablokowane: kręciłem się po plikach A i B."},
				{FinishReason: "stop"},
			},
		},
	}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name: "read_lines", Description: "read", Schema: `{"type":"object"}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "lines"}, nil
		},
	})
	// Always-on so thin protocol still advertises something before force.
	reg.MarkAlwaysOn("read_lines")
	l, err := NewLoop(LoopConfig{
		Provider: p, Registry: reg, MaxSteps: 10, ThinTools: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := l.Run(context.Background(), "znajdź bug")
	if err != nil {
		t.Fatal(err)
	}
	events := drainEvents(t, ch)

	var forcedNotice bool
	for _, ev := range events {
		if n, ok := ev.(NoticeEvent); ok && strings.Contains(n.Text, "tools off") {
			forcedNotice = true
		}
	}
	if !forcedNotice {
		t.Fatalf("want force-reply notice with tools off; events=%#v counts=%v", events, p.toolCounts)
	}

	p.mu.Lock()
	counts := append([]int(nil), p.toolCounts...)
	p.mu.Unlock()
	if len(counts) < 5 {
		t.Fatalf("want ≥5 Complete calls, got %v", counts)
	}
	// First 4 discovery turns had tools; 5th (force reply) must be 0.
	if counts[4] != 0 {
		t.Fatalf("force-reply Complete tool count = %d, want 0; all=%v", counts[4], counts)
	}
	// Earlier turns must have had tools (read_lines always-on).
	for i := 0; i < 4; i++ {
		if counts[i] == 0 {
			t.Fatalf("turn %d unexpectedly had 0 tools before force", i)
		}
	}
	// The forced answer must not be a dead end: within the SAME Run the loop
	// hands the tools back once, so work can continue without the user having
	// to type again.
	if len(counts) < 6 || counts[5] == 0 {
		t.Fatalf("tools were not re-armed after the forced answer; counts=%v", counts)
	}
	var resumed bool
	for _, ev := range events {
		if n, ok := ev.(NoticeEvent); ok && strings.Contains(n.Text, "tools re-enabled") {
			resumed = true
		}
	}
	if !resumed {
		t.Fatalf("want a tools-re-enabled notice after the forced answer; events=%#v", events)
	}
	done, ok := events[len(events)-1].(DoneEvent)
	if !ok {
		t.Fatalf("last = %#v, want DoneEvent", events[len(events)-1])
	}
	if done.Steps < 5 {
		t.Fatalf("steps=%d, want ≥5", done.Steps)
	}
}

func TestLoop_ForceReplyClearsForNextUserRun(t *testing.T) {
	// After a forced-reply Run ends, a new Run must restore tools.
	p := &toolCountProvider{
		scripts: [][]llm.Delta{
			// Run 1: cycle then force reply
			{
				{Role: llm.RoleAssistant},
				{ToolCall: &llm.ToolCall{ID: "1", Name: "read_lines", Arguments: `{"file":"a"}`}},
				{FinishReason: "tool_calls"},
			},
			{
				{Role: llm.RoleAssistant},
				{ToolCall: &llm.ToolCall{ID: "2", Name: "read_lines", Arguments: `{"file":"b"}`}},
				{FinishReason: "tool_calls"},
			},
			{
				{Role: llm.RoleAssistant},
				{ToolCall: &llm.ToolCall{ID: "3", Name: "read_lines", Arguments: `{"file":"a"}`}},
				{FinishReason: "tool_calls"},
			},
			{
				{Role: llm.RoleAssistant},
				{ToolCall: &llm.ToolCall{ID: "4", Name: "read_lines", Arguments: `{"file":"b"}`}},
				{FinishReason: "tool_calls"},
			},
			{
				{Role: llm.RoleAssistant, Content: "koniec pętli"},
				{FinishReason: "stop"},
			},
			// Run 2: plain answer — must see tools again
			{
				{Role: llm.RoleAssistant, Content: "ok"},
				{FinishReason: "stop"},
			},
		},
	}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name: "read_lines", Description: "read", Schema: `{"type":"object"}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "ok"}, nil
		},
	})
	reg.MarkAlwaysOn("read_lines")
	l, err := NewLoop(LoopConfig{
		Provider: p, Registry: reg, MaxSteps: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	ch1, _ := l.Run(context.Background(), "run1")
	drainEvents(t, ch1)
	ch2, _ := l.Run(context.Background(), "run2")
	drainEvents(t, ch2)

	p.mu.Lock()
	counts := append([]int(nil), p.toolCounts...)
	p.mu.Unlock()
	if len(counts) < 6 {
		t.Fatalf("want ≥6 completes, got %v", counts)
	}
	// Last call is run2 first (and only) turn — tools restored.
	last := counts[len(counts)-1]
	if last == 0 {
		t.Fatalf("next user Run still has 0 tools; counts=%v", counts)
	}
	// Force reply turn in run1 had 0 tools.
	if counts[4] != 0 {
		t.Fatalf("force reply turn tools=%d want 0; counts=%v", counts[4], counts)
	}
}

func TestDiscoveryProgress_Period3Cycle(t *testing.T) {
	var p discoveryProgress
	a := llm.ToolCall{Name: "read_lines", Arguments: `{"file":"a"}`}
	b := llm.ToolCall{Name: "read_lines", Arguments: `{"file":"b"}`}
	c := llm.ToolCall{Name: "read_lines", Arguments: `{"file":"c"}`}
	for _, call := range []llm.ToolCall{a, b, c, a, b} {
		p.observe([]llm.ToolCall{call}, allOK(1))
	}
	if sig := p.observe([]llm.ToolCall{c}, allOK(1)); sig != discoveryForceReply {
		t.Fatalf("A-B-C-A-B-C should force, got %v", sig)
	}
}

// Force-reply turn: model hallucinates a tool_call despite toolDefs=nil.
// Must NOT execute, must NOT leave assistant tool_call without tool_result,
// and must still finish with a user-facing text answer.
func TestLoop_ForceReplyDropsHallucinatedToolCalls(t *testing.T) {
	var execCount int
	p := &toolCountProvider{
		scripts: [][]llm.Delta{
			{
				{Role: llm.RoleAssistant},
				{ToolCall: &llm.ToolCall{ID: "1", Name: "read_lines", Arguments: `{"file":"a"}`}},
				{FinishReason: "tool_calls"},
			},
			{
				{Role: llm.RoleAssistant},
				{ToolCall: &llm.ToolCall{ID: "2", Name: "read_lines", Arguments: `{"file":"b"}`}},
				{FinishReason: "tool_calls"},
			},
			{
				{Role: llm.RoleAssistant},
				{ToolCall: &llm.ToolCall{ID: "3", Name: "read_lines", Arguments: `{"file":"a"}`}},
				{FinishReason: "tool_calls"},
			},
			{
				{Role: llm.RoleAssistant},
				{ToolCall: &llm.ToolCall{ID: "4", Name: "read_lines", Arguments: `{"file":"b"}`}},
				{FinishReason: "tool_calls"},
			},
			// Force-reply: hallucinated tool call + no text
			{
				{Role: llm.RoleAssistant},
				{ToolCall: &llm.ToolCall{ID: "halluc", Name: "read_lines", Arguments: `{"file":"c"}`}},
				{FinishReason: "tool_calls"},
			},
			// Tools come back after the forced answer; the model has nothing
			// left to do and finishes with text.
			{
				{Role: llm.RoleAssistant, Content: "Zablokowane, kończę."},
				{FinishReason: "stop"},
			},
		},
	}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name: "read_lines", Description: "read", Schema: `{"type":"object"}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			execCount++
			return tools.Result{Text: "lines"}, nil
		},
	})
	reg.MarkAlwaysOn("read_lines")
	l, err := NewLoop(LoopConfig{
		Provider: p, Registry: reg, MaxSteps: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := l.Run(context.Background(), "loop")
	if err != nil {
		t.Fatal(err)
	}
	events := drainEvents(t, ch)

	// Exactly 4 real executions (A B A B). Hallucinated 5th must not run.
	if execCount != 4 {
		t.Fatalf("read_lines executed %d times, want 4 (hallucinated force-reply call must not run)", execCount)
	}

	// No ToolCallEvent for the hallucinated id after force.
	for _, ev := range events {
		if tc, ok := ev.(ToolCallEvent); ok && tc.ID == "halluc" {
			t.Fatalf("hallucinated tool call was announced to UI: %#v", tc)
		}
		if tr, ok := ev.(ToolResultEvent); ok && tr.ID == "halluc" {
			t.Fatalf("hallucinated tool result event: %#v", tr)
		}
	}

	// History: no orphan assistant tool_call without tool_result.
	// After force, last assistant must be text-only (or fallback text).
	var lastAsst *llm.Message
	for i := range l.Messages {
		if l.Messages[i].Role == llm.RoleAssistant {
			lastAsst = &l.Messages[i]
		}
	}
	if lastAsst == nil {
		t.Fatal("no assistant message after force reply")
	}
	if len(lastAsst.ToolCalls) != 0 {
		t.Fatalf("assistant still has ToolCalls after force drop: %#v", lastAsst.ToolCalls)
	}
	// Fallback text when model returned empty + hallucinated tools.
	body := strings.TrimSpace(lastAsst.Content)
	if body == "" && len(lastAsst.Parts) > 0 {
		body = strings.TrimSpace(lastAsst.Parts[0].Text)
	}
	if body == "" {
		t.Fatal("want non-empty forced reply text in history")
	}

	p.mu.Lock()
	counts := append([]int(nil), p.toolCounts...)
	p.mu.Unlock()
	if len(counts) < 5 || counts[4] != 0 {
		t.Fatalf("force-reply Complete must have 0 tools; counts=%v", counts)
	}

	if _, ok := events[len(events)-1].(DoneEvent); !ok {
		t.Fatalf("last = %#v, want DoneEvent (controlled finish, not hang)", events[len(events)-1])
	}
}
