package agent

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestLoop_AdaptiveReflectionSkipsHealthyRun(t *testing.T) {
	prov := &adaptiveReflectionProvider{calls: []llm.ToolCall{
		{ID: "c1", Name: "probe", Arguments: `{"n":1}`},
		{ID: "c2", Name: "probe", Arguments: `{"n":2}`},
		{ID: "c3", Name: "probe", Arguments: `{"n":3}`},
		{ID: "c4", Name: "probe", Arguments: `{"n":4}`},
		{ID: "c5", Name: "probe", Arguments: `{"n":5}`},
	}}
	reflector := &stubReflector{text: "should not run"}
	loop := newAdaptiveReflectionLoop(t, prov, reflector, 10, false)
	events, _ := loop.Run(context.Background(), "start")
	drainEvents(t, events)
	if got := atomic.LoadInt32(&reflector.calls); got != 0 {
		t.Fatalf("healthy run reflections = %d, want 0", got)
	}
}

func TestLoop_AdaptiveReflectionOnRepeatedBatch(t *testing.T) {
	call := llm.ToolCall{ID: "same", Name: "probe", Arguments: `{"n":1}`}
	prov := &adaptiveReflectionProvider{calls: []llm.ToolCall{call, call}}
	reflector := &stubReflector{text: "change approach"}
	loop := newAdaptiveReflectionLoop(t, prov, reflector, 10, false)
	events, _ := loop.Run(context.Background(), "start")
	evs := drainEvents(t, events)
	assertReflectionReason(t, evs, "repeated_tool_batch", 2)
}

func TestLoop_AdaptiveReflectionOnRepeatedFailures(t *testing.T) {
	prov := &adaptiveReflectionProvider{calls: []llm.ToolCall{
		{ID: "c1", Name: "probe", Arguments: `{"n":1}`},
		{ID: "c2", Name: "probe", Arguments: `{"n":2}`},
	}}
	reflector := &stubReflector{text: "use the diagnostic"}
	loop := newAdaptiveReflectionLoop(t, prov, reflector, 10, true)
	events, _ := loop.Run(context.Background(), "start")
	evs := drainEvents(t, events)
	assertReflectionReason(t, evs, "repeated_tool_failure", 2)
}

func TestLoop_AdaptiveReflectionBeforeStepLimit(t *testing.T) {
	prov := &adaptiveReflectionProvider{calls: []llm.ToolCall{
		{ID: "c1", Name: "probe", Arguments: `{"n":1}`},
		{ID: "c2", Name: "probe", Arguments: `{"n":2}`},
		{ID: "c3", Name: "probe", Arguments: `{"n":3}`},
	}}
	reflector := &stubReflector{text: "one final correction"}
	loop := newAdaptiveReflectionLoop(t, prov, reflector, 4, false)
	events, _ := loop.Run(context.Background(), "start")
	evs := drainEvents(t, events)
	assertReflectionReason(t, evs, "step_budget_low", 3)
}

func TestLoop_ReflectionTruncatesLongText(t *testing.T) {
	// A wordy reflector must not grow the conversation by its full text:
	// the injected message and the event are clamped to reflectionMaxChars.
	long := strings.Repeat("line one of the reflection. \n", 60) // > 400 chars
	prov := &reflectionTestProvider{calls: 6}
	reflector := &stubReflector{text: long}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name: "noop", Description: "no-op", Schema: `{}`,
		Fn: func(_ context.Context, _ json.RawMessage) (tools.Result, error) { return tools.Result{Text: "ok"}, nil },
	})
	loop, err := NewLoop(LoopConfig{
		Provider: prov, Registry: reg, System: "you are test",
		MaxSteps: 10, Reflector: reflector, ReflectEvery: 5,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	events, err := loop.Run(context.Background(), "start")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	evs := drainEvents(t, events)

	// Event text is clamped.
	var got *ReflectionEvent
	for _, e := range evs {
		if r, ok := e.(ReflectionEvent); ok {
			got = &r
		}
	}
	if got == nil {
		t.Fatal("no reflection event emitted")
	}
	if !strings.HasSuffix(got.Text, "[reflection truncated]") {
		t.Errorf("event text not marked truncated: %q", got.Text)
	}
	if len(got.Text) > reflectionMaxChars+len("\n[reflection truncated]") {
		t.Errorf("event text length = %d, want <= %d", len(got.Text), reflectionMaxChars+len("\n[reflection truncated]"))
	}

	// The persisted system message is clamped the same way.
	var saw bool
	for i := len(loop.Messages) - 1; i >= 0; i-- {
		m := loop.Messages[i]
		if m.Role == llm.RoleSystem && strings.Contains(m.Content, "reflection checkpoint @ step 5") {
			saw = true
			if !strings.Contains(m.Content, "[reflection truncated]") {
				t.Errorf("injected reflection not truncated: %.120q...", m.Content)
			}
			break
		}
	}
	if !saw {
		t.Error("no reflection system message found in Messages")
	}
}

func TestLoop_LoopWarningSuppressesDuplicateReflection(t *testing.T) {
	// Two turns of an identical two-call batch: the batch fingerprint repeats
	// (repeated_tool_batch) in the same step the [loop] warning fires for the
	// identical calls. The reflection must be suppressed — one system message
	// about the same event is enough — and the signal consumed.
	call := llm.ToolCall{ID: "same", Name: "probe", Arguments: `{"n":1}`}
	prov := &dupBatchProvider{turnBatches: [][]llm.ToolCall{
		{call, call},
		{call, call},
	}}
	reflector := &stubReflector{text: "change approach"}
	loop := newAdaptiveReflectionLoop(t, prov, reflector, 10, false)
	events, _ := loop.Run(context.Background(), "start")
	evs := drainEvents(t, events)

	var refEvents []ReflectionEvent
	for _, e := range evs {
		if r, ok := e.(ReflectionEvent); ok {
			refEvents = append(refEvents, r)
		}
	}
	if len(refEvents) != 0 {
		t.Fatalf("reflections = %d, want 0 (suppressed by [loop] warning): %+v", len(refEvents), refEvents)
	}
	if got := atomic.LoadInt32(&reflector.calls); got != 0 {
		t.Errorf("reflector calls = %d, want 0", got)
	}
	var sawLoop bool
	for _, m := range loop.Messages {
		if m.Role == llm.RoleSystem && strings.Contains(m.Content, "[loop]") {
			sawLoop = true
			break
		}
	}
	if !sawLoop {
		t.Error("no [loop] warning message injected")
	}
}

func newAdaptiveReflectionLoop(t *testing.T, prov llm.Provider, reflector Reflector, maxSteps int, fail bool) *Loop {
	t.Helper()
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name: "probe", Description: "test probe", Schema: `{}`,
		Fn: func(_ context.Context, _ json.RawMessage) (tools.Result, error) {
			if fail {
				return tools.Result{Err: errStr("probe failed")}, nil
			}
			return tools.Result{Text: "ok"}, nil
		},
	})
	loop, err := NewLoop(LoopConfig{
		Provider: prov, Registry: reg, MaxSteps: maxSteps,
		Reflector: reflector, ReflectEvery: 8, AdaptiveReflection: true,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	return loop
}

func assertReflectionReason(t *testing.T, events []Event, reason string, step int) {
	t.Helper()
	var got []ReflectionEvent
	for _, ev := range events {
		if reflection, ok := ev.(ReflectionEvent); ok {
			got = append(got, reflection)
		}
	}
	if len(got) != 1 {
		t.Fatalf("reflection events = %d, want 1 (%+v)", len(got), got)
	}
	if got[0].Reason != reason || got[0].Step != step {
		t.Fatalf("reflection = %+v, want reason=%q step=%d", got[0], reason, step)
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

type adaptiveReflectionProvider struct {
	calls    []llm.ToolCall
	provider reflectCounter
}

func (p *adaptiveReflectionProvider) Name() string         { return "test" }
func (p *adaptiveReflectionProvider) SupportsVision() bool { return false }
func (p *adaptiveReflectionProvider) Complete(_ context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	idx := int(atomic.AddInt32(&p.provider.n, 1)) - 1
	ch := make(chan llm.Delta, 2)
	go func() {
		defer close(ch)
		if idx < len(p.calls) {
			call := p.calls[idx]
			ch <- llm.Delta{Role: llm.RoleAssistant, ToolCall: &call}
			ch <- llm.Delta{FinishReason: "tool_calls"}
			return
		}
		ch <- llm.Delta{Role: llm.RoleAssistant, Content: "done"}
		ch <- llm.Delta{FinishReason: "stop"}
	}()
	return ch, nil
}

type reflectCounter struct{ n int32 }

// dupBatchProvider emits one fixed multi-call batch per turn, then a final
// text turn. Used to trigger the [loop] warning and repeated_tool_batch in
// the same step.
type dupBatchProvider struct {
	turnBatches [][]llm.ToolCall
	provider    reflectCounter
}

func (p *dupBatchProvider) Name() string         { return "test" }
func (p *dupBatchProvider) SupportsVision() bool { return false }
func (p *dupBatchProvider) Complete(_ context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	idx := int(atomic.AddInt32(&p.provider.n, 1)) - 1
	ch := make(chan llm.Delta, 4)
	go func() {
		defer close(ch)
		if idx < len(p.turnBatches) {
			for _, call := range p.turnBatches[idx] {
				c := call
				ch <- llm.Delta{Role: llm.RoleAssistant, ToolCall: &c}
			}
			ch <- llm.Delta{FinishReason: "tool_calls"}
			return
		}
		ch <- llm.Delta{Role: llm.RoleAssistant, Content: "done"}
		ch <- llm.Delta{FinishReason: "stop"}
	}()
	return ch, nil
}

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
			// Unique args so discovery cycle fuse does not force-reply
			// before ReflectEvery can fire (these tests measure reflection).
			ch <- llm.Delta{
				Role: llm.RoleAssistant,
				ToolCall: &llm.ToolCall{
					ID:        fmt.Sprintf("call_%d", idx),
					Name:      "noop",
					Arguments: fmt.Sprintf(`{"n":%d}`, idx),
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
