package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// 1.0.38: the automatic wake-up cap counts only consecutive automatic turns
// WITHOUT progress. In the user's orchestration (30 parallel workers, the
// coordinator refilling every freed slot) every wake-up started a worker,
// yet the old cap of 10 automatic turns held all further reports until the
// user wrote again. Fake providers and tools only.

// refillProvider answers user turns with text; an automatic turn starts the
// "refill" tool when refill is set, otherwise it only acknowledges.
type refillProvider struct {
	refill atomic.Bool
	calls  atomic.Int32
	mu     sync.Mutex
	hints  []string
}

func (*refillProvider) Name() string { return "refill-fixture" }

func (p *refillProvider) Complete(_ context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	p.calls.Add(1)
	out := make(chan llm.Delta, 2)
	last := msgs[len(msgs)-1]
	var joined strings.Builder
	for _, m := range msgs {
		joined.WriteString(m.Content)
		joined.WriteString("\n")
	}
	if strings.Contains(joined.String(), "automatic wake-up") {
		p.mu.Lock()
		p.hints = append(p.hints, joined.String())
		p.mu.Unlock()
	}
	freshReport := false
	for i := len(msgs) - 1; i >= 0 && msgs[i].Role != llm.RoleAssistant && msgs[i].Role != llm.RoleTool; i-- {
		freshReport = freshReport || msgs[i].Name == llm.WorkerNotificationName
	}
	switch {
	case last.Role == llm.RoleTool:
		out <- llm.Delta{Content: "refilled", FinishReason: "stop"}
	case p.refill.Load() && freshReport:
		out <- llm.Delta{ToolCall: &llm.ToolCall{ID: "refill-" + time.Now().Format("150405.000000000"), Name: "refill", Arguments: `{}`}}
		out <- llm.Delta{FinishReason: "tool_calls"}
	default:
		out <- llm.Delta{Content: "noted", FinishReason: "stop"}
	}
	close(out)
	return out, nil
}

func newRefillLoop(t *testing.T, p *refillProvider, max int) (*Loop, chan Event, *atomic.Int32) {
	t.Helper()
	var refills atomic.Int32
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "refill", Description: "start a worker", Schema: `{"type":"object"}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			refills.Add(1)
			return tools.Result{Text: "worker started"}, nil
		}})
	reg.MarkAlwaysOn("refill")
	loop, err := NewLoop(LoopConfig{Provider: p, Registry: reg, MaxSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	ext := make(chan Event, 256)
	loop.SetExternalSink(ext)
	loop.SetBackgroundWake(WakeConfig{Debounce: testWakeDebounce, MaxConsecutive: 2})
	return loop, ext, &refills
}

func TestWakeCapIgnoresAutomaticTurnsWithProgress(t *testing.T) {
	p := &refillProvider{}
	p.refill.Store(true)
	loop, ext, refills := newRefillLoop(t, p, 2)
	drainRun(t, mustRun(t, loop, "keep the worker pool full: when an agent finishes, start a new one"))
	// Far more automatic turns than the cap: each one starts a worker.
	for i := 0; i < 6; i++ {
		deliverIdleReport(loop, "report")
		if _, ok := waitWakeEvent(t, ext, time.Second); !ok {
			t.Fatalf("wake %d missing: a turn that made progress counted against the cap", i+1)
		}
		events, err := loop.RunWake(context.Background(), true)
		if err != nil {
			t.Fatal(err)
		}
		drainRun(t, events)
	}
	if got := refills.Load(); got != 6 {
		t.Fatalf("refills = %d, want 6", got)
	}
	if c := loop.WakeScheduler().Consecutive(); c != 0 {
		t.Fatalf("consecutive no-progress turns = %d after progress", c)
	}
	// The user's goal is repeated in every automatic turn's hint.
	p.mu.Lock()
	hints := append([]string(nil), p.hints...)
	p.mu.Unlock()
	if len(hints) == 0 {
		t.Fatal("no automatic turn reached the provider")
	}
	for i, h := range hints {
		if !strings.Contains(h, "keep the worker pool full") || !strings.Contains(h, "Keep pursuing the user's stated goal") {
			t.Fatalf("wake hint %d lacks the user's goal:\n%s", i, h)
		}
	}
}

func TestWakeCapHoldsIdleTurnsAndResumeRestoresBudget(t *testing.T) {
	p := &refillProvider{}
	loop, ext, _ := newRefillLoop(t, p, 2)
	drainRun(t, mustRun(t, loop, "start"))
	for i := 0; i < 2; i++ {
		deliverIdleReport(loop, "report")
		if _, ok := waitWakeEvent(t, ext, time.Second); !ok {
			t.Fatalf("wake %d missing", i+1)
		}
		events, err := loop.RunWake(context.Background(), true)
		if err != nil {
			t.Fatal(err)
		}
		drainRun(t, events)
	}
	waitHeld := func(wantUnseen int, wantFirst bool) {
		t.Helper()
		deadline := time.After(time.Second)
		for {
			select {
			case ev := <-ext:
				switch ev := ev.(type) {
				case BackgroundWakeEvent:
					t.Fatal("woke past the cap of idle turns")
				case BackgroundWakeHeldEvent:
					if ev.Unseen == wantUnseen {
						if ev.First != wantFirst || ev.Limit != 2 {
							t.Fatalf("held event = %+v", ev)
						}
						return
					}
				}
			case <-deadline:
				t.Fatalf("no held notice for %d report(s)", wantUnseen)
			}
		}
	}
	deliverIdleReport(loop, "HELD_1")
	waitHeld(1, true)
	// A later report updates the count of the same hold (not a new notice).
	deliverIdleReport(loop, "HELD_2")
	waitHeld(2, false)
	if loop.WakeScheduler().Held() != 2 {
		t.Fatalf("held = %d", loop.WakeScheduler().Held())
	}
	// Resume (GUI button): budget restored, held reports wake right away.
	loop.WakeScheduler().Resume()
	if _, ok := waitWakeEvent(t, ext, time.Second); !ok {
		t.Fatal("resume did not wake the held reports")
	}
	if loop.WakeScheduler().Held() != 0 {
		t.Fatal("hold not cleared by resume")
	}
}

func TestWakeSchedulerReleaseHookAndProgressReset(t *testing.T) {
	var released atomic.Int32
	var heldCalls atomic.Int32
	state := WakeState{Unseen: 1}
	var started atomic.Int32
	s := NewWakeScheduler(WakeConfig{Debounce: time.Millisecond, MaxConsecutive: 1},
		func() WakeState { return state },
		func(WakeRequest) bool { started.Add(1); return true },
		func(int) { heldCalls.Add(1) })
	s.SetReleaseHook(func() { released.Add(1) })
	s.WakeStarted()
	s.WakeFinished(false) // idle turn: counts
	s.fire()
	if heldCalls.Load() != 1 || s.Held() != 1 || started.Load() != 0 {
		t.Fatalf("not held at cap: held=%d calls=%d started=%d", s.Held(), heldCalls.Load(), started.Load())
	}
	s.WakeFinished(true) // progress ends the hold
	if released.Load() != 1 || s.Held() != 0 || s.Consecutive() != 0 {
		t.Fatalf("progress did not end the hold: released=%d held=%d c=%d", released.Load(), s.Held(), s.Consecutive())
	}
	s.fire()
	if started.Load() != 1 {
		t.Fatal("no wake after progress restored the budget")
	}
	s.UserTurn() // no hold active: no release callback
	if released.Load() != 1 {
		t.Fatal("release hook fired without a hold")
	}
}

func TestWakeUserGoalIsCompactAndOrdered(t *testing.T) {
	long := strings.Repeat("x", 2000)
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "first goal"},
		{Role: llm.RoleAssistant, Content: "ok"},
		{Role: llm.RoleUser, Content: "second   goal\nwith lines"},
		{Role: llm.RoleUser, Name: llm.WorkerNotificationName, Content: "<task-notification>report</task-notification>"},
		{Role: llm.RoleUser, Content: long},
		{Role: llm.RoleUser, Content: "latest"},
	}
	got := wakeUserGoal(msgs)
	if strings.Contains(got, "first goal") || strings.Contains(got, "task-notification") {
		t.Fatalf("goal block = %q", got)
	}
	i, j, k := strings.Index(got, "second goal with lines"), strings.Index(got, "xxxx"), strings.Index(got, "latest")
	if i < 0 || j < 0 || k < 0 || !(i < j && j < k) {
		t.Fatalf("not oldest-first: %q", got)
	}
	if len([]rune(got)) > 3*wakeGoalRunes+400 {
		t.Fatalf("goal block not compact: %d runes", len([]rune(got)))
	}
	if wakeUserGoal(nil) != "" {
		t.Fatal("empty history produced a goal block")
	}
}

// A worker whose model is still writing a large call shows phase "args" with
// the streamed size; the tool phase starts only when the call executes.
type argsStreamingProvider struct {
	gate  chan struct{}
	calls atomic.Int32
}

func (*argsStreamingProvider) Name() string { return "args-fixture" }

func (p *argsStreamingProvider) Complete(ctx context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	n := p.calls.Add(1)
	out := make(chan llm.Delta, 4)
	go func() {
		defer close(out)
		if n == 1 {
			out <- llm.Delta{OutputStarted: true}
			out <- llm.Delta{ToolArgs: &llm.ToolArgsProgress{Name: "create_file", Bytes: 7351}}
			select { // the model is still writing the file
			case <-p.gate:
			case <-ctx.Done():
				return
			}
			out <- llm.Delta{ToolCall: &llm.ToolCall{ID: "cf-1", Name: "create_file", Arguments: `{"path":"a.zig","content":"x"}`}}
			out <- llm.Delta{FinishReason: "tool_calls"}
			return
		}
		out <- llm.Delta{Content: "ARGS_REPORT", FinishReason: "stop"}
	}()
	return out, nil
}

func TestWorkerLivenessShowsModelWritingCallArguments(t *testing.T) {
	prov := &argsStreamingProvider{gate: make(chan struct{})}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "create_file", Description: "create", Schema: `{"type":"object"}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{Text: "Created a.zig"}, nil }})
	reg.MarkAlwaysOn("create_file")
	loop, err := NewLoop(LoopConfig{Provider: prov, Registry: reg, MaxSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	w := NewWorkerRegistry().Add("code", "ARGS_TASK", loop)
	var mu sync.Mutex
	var progress []WorkerProgressEvent
	w.progress = func(ev WorkerProgressEvent) {
		mu.Lock()
		progress = append(progress, ev)
		mu.Unlock()
	}
	done := make(chan error, 1)
	go func() {
		_, err := runWorkerInvocation(context.Background(), w, "ARGS_TASK", nil)
		done <- err
	}()
	waitFor(t, "args phase", func() bool { return w.Snapshot().Live.Phase == "args" })
	if l := w.Snapshot().Live; l.Tool != "create_file" || l.ArgsBytes != 7351 || l.OpenCalls != 0 {
		t.Fatalf("args phase = %+v", l)
	}
	mu.Lock()
	var sawArgs bool
	for _, ev := range progress {
		sawArgs = sawArgs || (ev.Kind == "tool_args" && ev.Tool == "create_file" && ev.Bytes == 7351)
	}
	mu.Unlock()
	if !sawArgs {
		t.Fatalf("tool_args progress not forwarded: %+v", progress)
	}
	close(prov.gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if s := w.Snapshot(); s.Status != "done" || s.Live.Phase != "" {
		t.Fatalf("finished worker = %q %+v", s.Status, s.Live)
	}
}
