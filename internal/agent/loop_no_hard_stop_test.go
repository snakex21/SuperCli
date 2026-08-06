package agent

import (
	"context"
	"encoding/json"
	"strconv"
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

// The regression this file exists for: a long, healthy task is nothing but a
// long series of different, successful tool calls. Two hundred of them must
// not end the turn, must not take the model's tools away, and must not make
// the user type "continue".
func TestLoop_LongSuccessfulRunKeepsToolsAndFinishesNormally(t *testing.T) {
	const n = 200
	scripts := make([][]llm.Delta, 0, n+1)
	for i := 0; i < n; i++ {
		scripts = append(scripts, []llm.Delta{
			{Role: llm.RoleAssistant},
			{ToolCall: &llm.ToolCall{
				ID:        strconv.Itoa(i),
				Name:      "read_lines",
				Arguments: `{"file":"f` + strconv.Itoa(i) + `.go"}`,
			}},
			{FinishReason: "tool_calls"},
		})
	}
	scripts = append(scripts, []llm.Delta{
		{Role: llm.RoleAssistant, Content: "Gotowe."},
		{FinishReason: "stop"},
	})

	p := &toolCountProvider{scripts: scripts}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name: "read_lines", Description: "read", Schema: `{"type":"object"}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "lines"}, nil
		},
	})
	reg.MarkAlwaysOn("read_lines")

	l, err := NewLoop(LoopConfig{Provider: p, Registry: reg, ThinTools: false})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := l.Run(context.Background(), "przeczytaj cały projekt")
	if err != nil {
		t.Fatal(err)
	}
	events := drainEvents(t, ch)

	for _, ev := range events {
		if e, ok := ev.(ErrorEvent); ok {
			t.Fatalf("unexpected ErrorEvent after %d successful calls: %v", n, e.Err)
		}
	}
	done, ok := events[len(events)-1].(DoneEvent)
	if !ok {
		t.Fatalf("last = %#v, want DoneEvent", events[len(events)-1])
	}
	if done.Steps < n+1 {
		t.Fatalf("steps=%d, want ≥%d", done.Steps, n+1)
	}

	p.mu.Lock()
	counts := append([]int(nil), p.toolCounts...)
	p.mu.Unlock()
	if len(counts) < n+1 {
		t.Fatalf("want ≥%d Complete calls, got %d", n+1, len(counts))
	}
	for i, c := range counts {
		if c == 0 {
			t.Fatalf("turn %d was sent zero tool defs — tools must never be taken away", i)
		}
	}
}

// A real loop (same call, same arguments) must be answered with an injected
// error and MORE work, not with the end of the turn.
func TestLoop_RealLoopWarnsAndKeepsWorking(t *testing.T) {
	repeat := []llm.Delta{
		{Role: llm.RoleAssistant},
		{ToolCall: &llm.ToolCall{ID: "x", Name: "read_lines", Arguments: `{"file":"a"}`}},
		{FinishReason: "tool_calls"},
	}
	p := &toolCountProvider{scripts: [][]llm.Delta{
		repeat, repeat, repeat, repeat,
		{
			{Role: llm.RoleAssistant, Content: "Zmieniam podejście."},
			{FinishReason: "stop"},
		},
	}}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name: "read_lines", Description: "read", Schema: `{"type":"object"}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "lines"}, nil
		},
	})
	reg.MarkAlwaysOn("read_lines")

	l, err := NewLoop(LoopConfig{Provider: p, Registry: reg, ThinTools: false})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := l.Run(context.Background(), "czytaj")
	if err != nil {
		t.Fatal(err)
	}
	events := drainEvents(t, ch)

	warned := false
	for _, ev := range events {
		if n, ok := ev.(NoticeEvent); ok && strings.Contains(n.Text, "loop warning") {
			warned = true
		}
		if e, ok := ev.(ErrorEvent); ok {
			t.Fatalf("a warned loop must not end the turn: %v", e.Err)
		}
	}
	if !warned {
		t.Fatalf("want a loop warning notice; events=%#v", events)
	}
	if _, ok := events[len(events)-1].(DoneEvent); !ok {
		t.Fatalf("last = %#v, want DoneEvent", events[len(events)-1])
	}
	// The warning is injected into the conversation, so the model sees it.
	found := false
	for _, m := range l.Messages {
		if m.Role == llm.RoleSystem && strings.Contains(m.Content, "[loop]") {
			found = true
		}
	}
	if !found {
		t.Fatal("loop warning was not injected into the conversation")
	}

	p.mu.Lock()
	counts := append([]int(nil), p.toolCounts...)
	p.mu.Unlock()
	for i, c := range counts {
		if c == 0 {
			t.Fatalf("turn %d lost its tools during a warned loop; counts=%v", i, counts)
		}
	}
}

func TestRepeatProgress_NovelCallsNeverSignal(t *testing.T) {
	var p repeatProgress
	names := []string{"read_lines", "search_code", "list_dir", "read_many", "code_intel"}
	for i := 0; i < 500; i++ {
		batch := []llm.ToolCall{{
			Name:      names[i%len(names)],
			Arguments: `{"path":"f` + strconv.Itoa(i) + `.go"}`,
		}}
		if sig := p.observe(batch, allOK(1)); sig != repeatNone {
			t.Fatalf("call %d: got %v, want none — different calls are the job", i+1, sig)
		}
	}
}

func TestRepeatProgress_WarnsOnCycleAbortsOnAbsurdRepetition(t *testing.T) {
	var p repeatProgress
	a := []llm.ToolCall{{Name: "read_lines", Arguments: `{"file":"a"}`}}
	b := []llm.ToolCall{{Name: "read_lines", Arguments: `{"file":"b"}`}}
	// A-B-A-B is a period-2 cycle: warn, never stop.
	p.observe(a, allOK(1))
	p.observe(b, allOK(1))
	p.observe(a, allOK(1))
	if sig := p.observe(b, allOK(1)); sig != repeatWarn {
		t.Fatalf("A-B-A-B: got %v, want warn", sig)
	}

	var q repeatProgress
	warns, aborts := 0, 0
	for i := 0; i < repeatHardLimit+5; i++ {
		switch q.observe(a, allOK(1)) {
		case repeatWarn:
			warns++
		case repeatAbort:
			aborts++
		}
	}
	if warns == 0 {
		t.Fatal("an identical call repeated must warn before it aborts")
	}
	if aborts == 0 {
		t.Fatalf("identical call repeated %d times must eventually abort", repeatHardLimit+5)
	}
	// The abort must not fire early: below the hard limit it is only a warning.
	var r repeatProgress
	for i := 0; i < repeatHardLimit-1; i++ {
		if r.observe(a, allOK(1)) == repeatAbort {
			t.Fatalf("aborted after %d identical calls, hard limit is %d", i+1, repeatHardLimit)
		}
	}
}
