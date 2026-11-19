package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

const testWakeDebounce = 20 * time.Millisecond

func waitWakeEvent(t *testing.T, ch <-chan Event, within time.Duration) (BackgroundWakeEvent, bool) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case ev := <-ch:
			if wake, ok := ev.(BackgroundWakeEvent); ok {
				return wake, true
			}
		case <-deadline:
			return BackgroundWakeEvent{}, false
		}
	}
}

func drainRun(t *testing.T, events <-chan Event) (done bool) {
	t.Helper()
	for ev := range events {
		switch ev := ev.(type) {
		case DoneEvent:
			done = true
		case ErrorEvent:
			t.Errorf("run error: %v", ev.Err)
		}
	}
	return done
}

func requestContains(msgs []llm.Message, needle string) bool {
	for _, m := range msgs {
		if strings.Contains(m.Content, needle) || strings.Contains(m.TextOnly().Content, needle) {
			return true
		}
	}
	return false
}

// deliverIdleReport mimics startBackgroundWorker's delivery from a worker
// goroutine after the parent turn has ended.
func deliverIdleReport(loop *Loop, text string) {
	loop.backgroundDelivery(context.Background())(context.Background(), "<task-notification><status>completed</status><result>"+text+"</result></task-notification>")
}

// Reproduction of the reported bug: the coordinator started an async worker
// and its turn ended before the worker finished. Before the fix the late
// report was only appended to history and no model turn followed, so the
// user never heard about it. Now the turn ends without blocking (the user can
// keep talking) and the report wakes the idle conversation.
func TestBackgroundReportAfterTurnEndedWakesCoordinator(t *testing.T) {
	reg := tools.NewRegistry()
	coordinator := makeScriptedProvider("unused")
	coordinator.scripts = [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "bg", Name: "task", Arguments: `{"prompt":"review the fixture","async":true}`}, FinishReason: "tool_calls"}},
		{{Content: "Started a background review.", FinishReason: "stop"}},
		{{Content: "The review finished: LATE_FINDING applied.", FinishReason: "stop"}},
	}
	writer := &recordingWriter{}
	parent, err := NewLoop(LoopConfig{Provider: coordinator, Registry: reg, Writer: writer, MaxSteps: 6})
	if err != nil {
		t.Fatal(err)
	}
	child := makeScriptedProvider("LATE_FINDING: the fixture needs one fix")
	release := make(chan struct{})
	child.onCalled = func(int) { <-release }
	specs := NewSubAgentRegistry()
	MustRegisterAll(specs, BuiltinSubAgents())
	task, err := NewAgentTool(specs, parent, reg, child, nil, NewLoop)
	if err != nil {
		t.Fatal(err)
	}
	reg.MustRegister(task.Spec())
	reg.MarkAlwaysOn("task")
	ext := make(chan Event, 64)
	parent.SetExternalSink(ext)
	parent.SetBackgroundWake(WakeConfig{Debounce: testWakeDebounce})

	events, err := parent.Run(context.Background(), "Delegate a review in the background.")
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan bool, 1)
	go func() { finished <- drainRun(t, events) }()
	select {
	case done := <-finished:
		if !done {
			t.Fatal("coordinator turn failed")
		}
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("coordinator turn blocked on its background worker")
	}
	if parent.ActiveBackgroundWorkers() != 1 {
		t.Fatalf("active workers = %d, want 1", parent.ActiveBackgroundWorkers())
	}
	callsBefore := atomic.LoadInt32(&coordinator.calls)
	close(release)
	wake, ok := waitWakeEvent(t, ext, 5*time.Second)
	if !ok {
		t.Fatal("finished background worker did not wake the idle coordinator")
	}
	if wake.Unseen != 1 {
		t.Fatalf("wake unseen = %d", wake.Unseen)
	}
	persistedBefore := len(writer.messages)
	wakeEvents, err := parent.RunWake(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !drainRun(t, wakeEvents) {
		t.Fatal("automatic turn did not complete")
	}
	if atomic.LoadInt32(&coordinator.calls) != callsBefore+1 {
		t.Fatalf("automatic turn calls = %d", atomic.LoadInt32(&coordinator.calls)-callsBefore)
	}
	last := coordinator.reqs[len(coordinator.reqs)-1]
	if !requestContains(last, "LATE_FINDING") || !requestContains(last, "automatic wake-up") {
		t.Fatal("automatic turn did not carry the report and the wake-up marker")
	}
	for _, m := range writer.messages[persistedBefore:] {
		if m.Role == llm.RoleUser && m.Name != llm.WorkerNotificationName {
			t.Fatalf("automatic turn persisted a fake user message: %q", m.Content)
		}
		if strings.Contains(m.Content, "automatic wake-up") {
			t.Fatal("request-only wake marker was persisted")
		}
	}
	if parent.UnseenBackgroundReports() != 0 || parent.ActiveBackgroundWorkers() != 0 {
		t.Fatalf("unseen=%d active=%d", parent.UnseenBackgroundReports(), parent.ActiveBackgroundWorkers())
	}
	if _, again := waitWakeEvent(t, ext, 10*testWakeDebounce); again {
		t.Fatal("a handled report woke the coordinator again")
	}
}

// The same late report without a host scheduler (batch mode) is still found by
// the original blocking wait; nothing regresses for hosts that cannot wake.
func TestBackgroundWorkerWithoutSchedulerKeepsBlockingWait(t *testing.T) {
	reg := tools.NewRegistry()
	coordinator := makeScriptedProvider("unused")
	coordinator.scripts = [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "bg", Name: "task", Arguments: `{"prompt":"x","async":true}`}, FinishReason: "tool_calls"}},
		{{Content: "Waiting.", FinishReason: "stop"}},
		{{Content: "Used it.", FinishReason: "stop"}},
	}
	parent, err := NewLoop(LoopConfig{Provider: coordinator, Registry: reg, MaxSteps: 6})
	if err != nil {
		t.Fatal(err)
	}
	specs := NewSubAgentRegistry()
	MustRegisterAll(specs, BuiltinSubAgents())
	task, err := NewAgentTool(specs, parent, reg, makeScriptedProvider("BATCH_REPORT"), nil, NewLoop)
	if err != nil {
		t.Fatal(err)
	}
	reg.MustRegister(task.Spec())
	reg.MarkAlwaysOn("task")
	drainRun(t, mustRun(t, parent, "go"))
	if !requestContains(coordinator.reqs[len(coordinator.reqs)-1], "BATCH_REPORT") {
		t.Fatal("blocking wait lost the report")
	}
}

// A report arriving while a turn runs is delivered at the next tool boundary
// and needs no extra automatic turn afterwards.
func TestBackgroundReportWhileBusyIsDeliveredInTurnWithoutWake(t *testing.T) {
	reg := tools.NewRegistry()
	started, resume := make(chan struct{}), make(chan struct{})
	reg.MustRegister(tools.Tool{Name: "fixture_read", Description: "read", Schema: "{}", ReadOnly: true, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		close(started)
		<-resume
		return tools.Result{Text: "ok"}, nil
	}})
	reg.MarkAlwaysOn("fixture_read")
	provider := makeScriptedProvider("unused")
	provider.scripts = [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "r", Name: "fixture_read", Arguments: "{}"}, FinishReason: "tool_calls"}},
		{{Content: "Done.", FinishReason: "stop"}},
	}
	loop, err := NewLoop(LoopConfig{Provider: provider, Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	ext := make(chan Event, 64)
	loop.SetExternalSink(ext)
	loop.SetBackgroundWake(WakeConfig{Debounce: testWakeDebounce})
	events := mustRun(t, loop, "Read it.")
	<-started
	deliverIdleReport(loop, "BUSY_REPORT")
	close(resume)
	drainRun(t, events)
	if !requestContains(provider.reqs[1], "BUSY_REPORT") {
		t.Fatal("busy turn did not receive the report at its tool boundary")
	}
	if _, woke := waitWakeEvent(t, ext, 10*testWakeDebounce); woke {
		t.Fatal("report already seen by the busy turn caused an automatic turn")
	}
}

func TestBackgroundWakeBatchesCloseCompletions(t *testing.T) {
	loop, err := NewLoop(LoopConfig{Provider: makeScriptedProvider("ok"), Registry: tools.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	loop.Messages = append(loop.Messages, llm.Message{Role: llm.RoleUser, Content: "start"})
	ext := make(chan Event, 64)
	loop.SetExternalSink(ext)
	loop.SetBackgroundWake(WakeConfig{Debounce: 80 * time.Millisecond})
	for _, r := range []string{"A", "B", "C"} {
		deliverIdleReport(loop, r)
		time.Sleep(10 * time.Millisecond)
	}
	wake, ok := waitWakeEvent(t, ext, 2*time.Second)
	if !ok || wake.Unseen != 3 {
		t.Fatalf("batched wake = %+v ok=%v", wake, ok)
	}
	if _, again := waitWakeEvent(t, ext, 200*time.Millisecond); again {
		t.Fatal("close completions produced more than one wake-up")
	}
}

func TestBackgroundWakeAllWaitsForLastWorker(t *testing.T) {
	loop, err := NewLoop(LoopConfig{Provider: makeScriptedProvider("ok"), Registry: tools.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	ext := make(chan Event, 64)
	loop.SetExternalSink(ext)
	loop.SetBackgroundWake(WakeConfig{Mode: WakeAll, Debounce: testWakeDebounce})
	loop.backgroundWorkerStarted()
	loop.backgroundWorkerStarted()
	deliverIdleReport(loop, "first")
	loop.backgroundWorkerFinished()
	if _, woke := waitWakeEvent(t, ext, 10*testWakeDebounce); woke {
		t.Fatal("wake-all mode woke while a worker was still running")
	}
	deliverIdleReport(loop, "second")
	loop.backgroundWorkerFinished()
	wake, ok := waitWakeEvent(t, ext, time.Second)
	if !ok || wake.Unseen != 2 {
		t.Fatalf("wake-all = %+v ok=%v", wake, ok)
	}
}

func TestBackgroundWakeOffNeverWakes(t *testing.T) {
	loop, err := NewLoop(LoopConfig{Provider: makeScriptedProvider("ok"), Registry: tools.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	ext := make(chan Event, 64)
	loop.SetExternalSink(ext)
	loop.SetBackgroundWake(WakeConfig{Mode: ParseWakeMode("off"), Debounce: testWakeDebounce})
	deliverIdleReport(loop, "x")
	if _, woke := waitWakeEvent(t, ext, 10*testWakeDebounce); woke {
		t.Fatal("off mode woke")
	}
	if loop.UnseenBackgroundReports() != 1 {
		t.Fatal("report was not kept for the next user turn")
	}
}

// Consecutive automatic turns are capped; further reports are held until the
// user writes again, which restores the budget.
func TestBackgroundWakeLoopCapHoldsUntilUserInput(t *testing.T) {
	provider := makeScriptedProvider("noted")
	loop, err := NewLoop(LoopConfig{Provider: provider, Registry: tools.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	ext := make(chan Event, 128)
	loop.SetExternalSink(ext)
	loop.SetBackgroundWake(WakeConfig{Debounce: testWakeDebounce, MaxConsecutive: 2})
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
	deliverIdleReport(loop, "HELD_REPORT")
	held := false
	deadline := time.After(time.Second)
	for !held {
		select {
		case ev := <-ext:
			switch ev := ev.(type) {
			case BackgroundWakeEvent:
				t.Fatal("cap exceeded: third consecutive automatic turn")
			case BackgroundWakeHeldEvent:
				held = ev.Unseen == 1 && ev.Limit == 2
			}
		case <-deadline:
			t.Fatal("no held notice at the cap")
		}
	}
	calls := atomic.LoadInt32(&provider.calls)
	drainRun(t, mustRun(t, loop, "what happened?"))
	if !requestContains(provider.reqs[calls], "HELD_REPORT") {
		t.Fatal("held report missing from the next user turn")
	}
	deliverIdleReport(loop, "after user")
	if _, ok := waitWakeEvent(t, ext, time.Second); !ok {
		t.Fatal("user input did not restore the wake budget")
	}
}

type failingCompleteProvider struct{ *stubProvider }

func (p failingCompleteProvider) Complete(context.Context, []llm.Message, []llm.ToolDef) (<-chan llm.Delta, error) {
	return nil, errors.New("WORKER_BACKEND_DOWN")
}

func TestFailedBackgroundWorkerAlsoWakes(t *testing.T) {
	reg := tools.NewRegistry()
	parent, err := NewLoop(LoopConfig{Provider: makeScriptedProvider("ok"), Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	parent.Messages = append(parent.Messages, llm.Message{Role: llm.RoleUser, Content: "start"})
	specs := NewSubAgentRegistry()
	MustRegisterAll(specs, BuiltinSubAgents())
	task, err := NewAgentTool(specs, parent, reg, failingCompleteProvider{makeScriptedProvider("x")}, nil, NewLoop)
	if err != nil {
		t.Fatal(err)
	}
	ext := make(chan Event, 64)
	parent.SetExternalSink(ext)
	parent.SetBackgroundWake(WakeConfig{Debounce: testWakeDebounce})
	result, err := task.execute(context.Background(), json.RawMessage(`{"prompt":"do it","async":true}`))
	if err != nil || result.Err != nil {
		t.Fatalf("%v %v", err, result.Err)
	}
	if _, ok := waitWakeEvent(t, ext, 5*time.Second); !ok {
		t.Fatal("failed worker did not wake the coordinator")
	}
	found := false
	for _, m := range parent.Messages {
		if m.Name == llm.WorkerNotificationName && strings.Contains(m.Content, "WORKER_BACKEND_DOWN") {
			found = true
		}
	}
	if !found {
		t.Fatal("failure report missing from the conversation")
	}
}

func TestRunWakeWithoutReportsIsRefused(t *testing.T) {
	loop, err := NewLoop(LoopConfig{Provider: makeScriptedProvider("ok"), Registry: tools.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loop.RunWake(context.Background(), true); !errors.Is(err, ErrNothingToWake) {
		t.Fatalf("err = %v", err)
	}
}

func TestWakeSchedulerSkipsBusyAndResignals(t *testing.T) {
	var mu sync.Mutex
	state := WakeState{Unseen: 1, Busy: true}
	started := make(chan WakeRequest, 4)
	s := NewWakeScheduler(WakeConfig{Debounce: testWakeDebounce}, func() WakeState { mu.Lock(); defer mu.Unlock(); return state }, func(r WakeRequest) bool { started <- r; return true }, nil)
	s.Signal()
	select {
	case <-started:
		t.Fatal("woke a busy conversation")
	case <-time.After(10 * testWakeDebounce):
	}
	mu.Lock()
	state.Busy = false
	mu.Unlock()
	s.Signal()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("no wake once idle")
	}
	if ParseWakeMode("all") != WakeAll || ParseWakeMode("") != WakeEach || ParseWakeMode("OFF") != WakeOff {
		t.Fatal("mode parsing")
	}
}
