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

// stubReflector returns the configured text and counts calls.
type stubReflector struct {
	text  string
	calls int32
}

func (r *stubReflector) Reflect(_ context.Context, _ []llm.Message) (string, error) {
	atomic.AddInt32(&r.calls, 1)
	return r.text, nil
}

type failReflector struct{ err error }

func (r *failReflector) Reflect(_ context.Context, _ []llm.Message) (string, error) {
	return "", r.err
}

type stubInjector struct {
	text  string
	calls int32
}

func (i *stubInjector) Build(_ context.Context, _ string) (string, error) {
	atomic.AddInt32(&i.calls, 1)
	return i.text, nil
}

func TestLoop_ReflectionInjectedEvery5(t *testing.T) {
	// Provider: 5 model calls, each emitting a tool_call for
	// a trivial tool. Forces MaxSteps=5 and triggers
	// reflection after step 5 — but step 5 is the last so
	// we need a different setup. Use a provider that emits
	// 1 message then tool_calls 3 times, then text. We'll
	// have to make a richer provider here.

	// Simpler: 6 model calls. First 5 emit tool_call so the
	// loop continues; the 6th emits text. Reflection fires
	// at step 5.
	prov := &reflectionTestProvider{calls: 6}
	reflector := &stubReflector{text: "I am on the right track"}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name:        "noop",
		Description: "no-op",
		Schema:      `{}`,
		Fn:          func(_ context.Context, _ json.RawMessage) (tools.Result, error) { return tools.Result{Text: "ok"}, nil },
	})
	loop, err := NewLoop(LoopConfig{
		Provider:     prov,
		Registry:     reg,
		System:       "you are test",
		MaxSteps:     10,
		Reflector:    reflector,
		ReflectEvery: 5,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	events, err := loop.Run(context.Background(), "start")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	evs := drainEvents(t, events)

	// Verify: 1 reflection event emitted, at step 5.
	var refEvents []ReflectionEvent
	for _, e := range evs {
		if r, ok := e.(ReflectionEvent); ok {
			refEvents = append(refEvents, r)
		}
	}
	if len(refEvents) != 1 {
		t.Errorf("reflection events = %d, want 1", len(refEvents))
	} else if refEvents[0].Step != 5 {
		t.Errorf("reflection step = %d, want 5", refEvents[0].Step)
	} else if !strings.Contains(refEvents[0].Text, "right track") {
		t.Errorf("reflection text = %q", refEvents[0].Text)
	}

	// Verify: a system message was appended to Messages at
	// the end. Walk from the back; the last system message
	// should be our reflection.
	var saw bool
	for i := len(loop.Messages) - 1; i >= 0; i-- {
		m := loop.Messages[i]
		if m.Role == llm.RoleSystem && strings.Contains(m.Content, "reflection checkpoint @ step 5") {
			saw = true
			if !strings.Contains(m.Content, "right track") {
				t.Errorf("reflection message missing body: %q", m.Content)
			}
			break
		}
	}
	if !saw {
		t.Error("no reflection system message found in Messages")
	}

	// Verify: reflector was called exactly once.
	if got := atomic.LoadInt32(&reflector.calls); got != 1 {
		t.Errorf("reflector calls = %d, want 1", got)
	}
}

func TestLoop_ReflectionDisabledWhenReflectEveryZero(t *testing.T) {
	prov := &reflectionTestProvider{calls: 6}
	reflector := &stubReflector{text: "should not run"}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name: "noop", Description: "no-op", Schema: `{}`,
		Fn: func(_ context.Context, _ json.RawMessage) (tools.Result, error) { return tools.Result{Text: "ok"}, nil },
	})
	loop, _ := NewLoop(LoopConfig{
		Provider:  prov,
		Registry:  reg,
		MaxSteps:  10,
		Reflector: reflector,
		// ReflectEvery omitted (0)
	})
	events, _ := loop.Run(context.Background(), "start")
	evs := drainEvents(t, events)
	for _, e := range evs {
		if _, ok := e.(ReflectionEvent); ok {
			t.Error("reflection fired despite ReflectEvery=0")
		}
	}
	if got := atomic.LoadInt32(&reflector.calls); got != 0 {
		t.Errorf("reflector calls = %d, want 0", got)
	}
}

func TestLoop_ReflectionErrorDoesNotAbort(t *testing.T) {
	prov := &reflectionTestProvider{calls: 6}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name: "noop", Description: "no-op", Schema: `{}`,
		Fn: func(_ context.Context, _ json.RawMessage) (tools.Result, error) { return tools.Result{Text: "ok"}, nil },
	})
	loop, _ := NewLoop(LoopConfig{
		Provider:     prov,
		Registry:     reg,
		MaxSteps:     10,
		Reflector:    &failReflector{err: errStr("reflector broke")},
		ReflectEvery: 5,
	})
	events, err := loop.Run(context.Background(), "start")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Loop should still complete (with a DoneEvent or
	// tool-call events) — not abort.
	evs := drainEvents(t, events)
	var sawDone bool
	for _, e := range evs {
		if _, ok := e.(DoneEvent); ok {
			sawDone = true
		}
	}
	if !sawDone {
		t.Errorf("expected DoneEvent despite reflection failure; got %d events", len(evs))
	}
}

func TestLoop_ReflectionEmptyIsNoop(t *testing.T) {
	prov := &reflectionTestProvider{calls: 6}
	reflector := &stubReflector{text: "   "} // empty after trim
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name: "noop", Description: "no-op", Schema: `{}`,
		Fn: func(_ context.Context, _ json.RawMessage) (tools.Result, error) { return tools.Result{Text: "ok"}, nil },
	})
	loop, _ := NewLoop(LoopConfig{
		Provider:     prov,
		Registry:     reg,
		MaxSteps:     10,
		Reflector:    reflector,
		ReflectEvery: 5,
	})
	events, _ := loop.Run(context.Background(), "start")
	evs := drainEvents(t, events)
	for _, e := range evs {
		if r, ok := e.(ReflectionEvent); ok {
			if r.Text != "" {
				t.Errorf("ReflectionEvent.Text = %q, want empty for trim-only input", r.Text)
			}
		}
	}
}

func TestLoop_PatternInjector_AppendsAtStart(t *testing.T) {
	prov := echoProvider("ok")
	reg := tools.NewRegistry()
	inj := &stubInjector{text: "## Patterns\n- search_code rg missing"}
	loop, err := NewLoop(LoopConfig{
		Provider:        prov,
		Registry:        reg,
		System:          "you are test",
		PatternInjector: inj,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	if len(loop.Messages) < 2 {
		t.Fatalf("Messages len = %d, want >= 2", len(loop.Messages))
	}
	// First message: system prompt. Second: patterns.
	if !strings.Contains(loop.Messages[1].Content, "Patterns") {
		t.Errorf("second message = %q, want patterns", loop.Messages[1].Content)
	}
	if got := atomic.LoadInt32(&inj.calls); got != 1 {
		t.Errorf("injector calls = %d, want 1", got)
	}
}

func TestLoop_PatternInjector_EmptyIsNoop(t *testing.T) {
	prov := echoProvider("ok")
	reg := tools.NewRegistry()
	inj := &stubInjector{text: ""}
	loop, _ := NewLoop(LoopConfig{
		Provider:        prov,
		Registry:        reg,
		System:          "sys",
		PatternInjector: inj,
	})
	// Only the system message, no extra.
	if len(loop.Messages) != 1 {
		t.Errorf("Messages len = %d, want 1 (system only)", len(loop.Messages))
	}
}

// reflectionTestProvider emits tool_calls for the first
// `calls` turns, then a final text turn. Mirrors the script
// provider shape in agent_tool_integration_test.go but
// tailored to F5.a.
type reflectionTestProvider struct {
	calls    int
	provider reflectCounter
}

type reflectCounter struct{ n int32 }

func (p *reflectionTestProvider) Name() string { return "test" }
func (p *reflectionTestProvider) SupportsVision() bool {
	return false
}
func (p *reflectionTestProvider) Complete(_ context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	idx := int(atomic.AddInt32(&p.provider.n, 1)) - 1
	ch := make(chan llm.Delta, 3)
	go func() {
		defer close(ch)
		if idx < p.calls {
			// Emit a tool_call so the loop continues.
			ch <- llm.Delta{
				Role: llm.RoleAssistant,
				ToolCall: &llm.ToolCall{
					ID:        "call_1",
					Name:      "noop",
					Arguments: `{}`,
				},
			}
			ch <- llm.Delta{FinishReason: "tool_calls"}
			return
		}
		// Final turn: text.
		ch <- llm.Delta{Role: llm.RoleAssistant, Content: "done"}
		ch <- llm.Delta{FinishReason: "stop", Usage: &llm.Usage{Input: 1, Output: 1, Total: 2}}
	}()
	return ch, nil
}

type rErr string

func (s rErr) Error() string { return string(s) }
func errStr(s string) error  { return rErr(s) }
