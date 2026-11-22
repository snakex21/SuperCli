package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// 1.0.36: every worker is always in a definite state with a reason. These
// fixtures use fake providers only (no network, no real model).

func definiteStateWorker(t *testing.T, store *memWorkerStore, prov llm.Provider) (*WorkerRegistry, *Worker) {
	t.Helper()
	loop, err := NewLoop(LoopConfig{Provider: prov, Registry: tools.NewRegistry(), MaxSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	reg := NewWorkerRegistry()
	if store != nil {
		reg.SetStore(store, nil)
	}
	w := reg.Add("code", "ORIGINAL_TASK: run the regression", loop)
	w.setState(func(w *Worker) { w.sessionID = "sess-1" })
	reg.persist(w)
	return reg, w
}

// A worker whose first run could not begin (here: its context was already
// canceled) used to stay "created" forever: counted as active, shown as
// running, with no runner. It is now failed, with the reason, and saved.
func TestNeverStartedWorkerIsFailedWithReason(t *testing.T) {
	store := newMemWorkerStore()
	reg, w := definiteStateWorker(t, store, makeScriptedProvider("unused"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	handoff, err := runWorkerInvocation(ctx, w, "ORIGINAL_TASK: run the regression", nil)
	if err == nil {
		t.Fatal("canceled start reported success")
	}
	s := w.Snapshot()
	if s.Status != "failed" || !strings.Contains(s.LastError, "did not start") || s.Runs != 0 {
		t.Fatalf("never-started worker = status %q runs %d err %q", s.Status, s.Runs, s.LastError)
	}
	if c := reg.Counts(); c.Running != 0 || c.Failed != 1 {
		t.Fatalf("never-started worker still counted as active: %+v", c)
	}
	rec, ok, _ := store.LoadWorker(context.Background(), "sess-1", w.ID)
	if !ok || rec.Status != "failed" || !strings.Contains(rec.LastError, "did not start") {
		t.Fatalf("saved record = %+v ok=%v", rec, ok)
	}
	if res := handoff.result(w, err); !strings.Contains(res.Text+errText(res.Err), "did not start") {
		t.Fatalf("coordinator notification lacks the reason: %q / %v", res.Text, res.Err)
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// The worker's own first run waits for a briefly held run lock (a concurrent
// send_message on a not-yet-started worker) instead of failing and leaving the
// worker "created" without a runner.
func TestFirstRunWaitsForBriefRunLock(t *testing.T) {
	_, w := definiteStateWorker(t, nil, makeScriptedProvider("REPORT_OK"))
	w.runMu.Lock()
	go func() {
		time.Sleep(50 * time.Millisecond)
		w.runMu.Unlock()
	}()
	handoff, err := runWorkerInvocation(context.Background(), w, "ORIGINAL_TASK: run the regression", nil)
	if err != nil {
		t.Fatalf("first run failed on a briefly held lock: %v", err)
	}
	if s := w.Snapshot(); s.Status != "done" || s.Runs != 1 || !strings.Contains(handoff.report, "REPORT_OK") {
		t.Fatalf("worker = %q runs %d report %q", s.Status, s.Runs, handoff.report)
	}
}

// send_message to a worker that has not started yet is refused without
// breaking the worker's own start.
func TestSendMessageToQueuedWorkerDoesNotStrandIt(t *testing.T) {
	store := newMemWorkerStore()
	reg, w := definiteStateWorker(t, store, makeScriptedProvider("REPORT_OK"))
	send := &SendMessageTool{Workers: reg}
	out, err := send.execute(context.Background(), json.RawMessage(`{"to":"`+w.ID+`","message":"also check X"}`))
	if err != nil || out.Err == nil || !strings.Contains(out.Err.Error(), "already running") {
		t.Fatalf("continuation of a queued worker = %v / %v", err, out.Err)
	}
	if s := w.Snapshot(); s.Status != "created" {
		t.Fatalf("refused follow-up changed the queued worker: %q", s.Status)
	}
	if _, err := runWorkerInvocation(context.Background(), w, "ORIGINAL_TASK", nil); err != nil {
		t.Fatal(err)
	}
	if s := w.Snapshot(); s.Status != "done" {
		t.Fatalf("queued worker did not run after the refused follow-up: %q", s.Status)
	}
}

// User data (worker-8, 1.0.35): the saved record said "created", runs=0 for
// the whole 21-minute run, so anything reading the store saw a worker that
// never started. The record now says "running" while it runs.
func TestRunningWorkerIsSavedAsRunning(t *testing.T) {
	store := newMemWorkerStore()
	child := makeScriptedProvider("REPORT_OK")
	release := make(chan struct{})
	started := make(chan struct{})
	child.onCalled = func(int) { close(started); <-release }
	_, w := definiteStateWorker(t, store, child)
	done := make(chan error, 1)
	go func() {
		_, err := runWorkerInvocation(context.Background(), w, "ORIGINAL_TASK", nil)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker never reached its provider")
	}
	rec, _, _ := store.LoadWorker(context.Background(), "sess-1", w.ID)
	if rec.Status != "running" || rec.Runs != 1 {
		close(release)
		t.Fatalf("saved record during the run = status %q runs %d, want running/1", rec.Status, rec.Runs)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	rec, _, _ = store.LoadWorker(context.Background(), "sess-1", w.ID)
	if rec.Status != "done" {
		t.Fatalf("saved record after the run = %q", rec.Status)
	}
}

// The registry is process-wide: another conversation's live worker with the
// same id must not keep this conversation's orphaned record "running".
func TestReconcileIgnoresSameIDWorkerOfAnotherConversation(t *testing.T) {
	store := newMemWorkerStore()
	_ = store.SaveWorker(context.Background(), WorkerRecord{SessionID: "sess-old", ID: "worker-1", Agent: "code", Description: "old",
		Status: "running", Messages: []llm.Message{{Role: llm.RoleUser, Content: "old"}}})
	reg := NewWorkerRegistry()
	reg.SetStore(store, nil)
	w := reg.Add("code", "live elsewhere", nil)
	w.setState(func(w *Worker) { w.sessionID, w.Status = "sess-new", "running" })
	changed, err := reg.ReconcileSavedWorkers(context.Background(), "sess-old")
	if err != nil || len(changed) != 1 || changed[0].Status != WorkerStatusInterrupted {
		t.Fatalf("orphan of another conversation kept as live: %+v %v", changed, err)
	}
}

// A never-started worker resumed by send_message receives its original task,
// not only the follow-up (it has no saved conversation).
func TestRetryOfNeverStartedWorkerCarriesOriginalTask(t *testing.T) {
	store := newMemWorkerStore()
	child := makeScriptedProvider("RETRIED_OK")
	reg, w := definiteStateWorker(t, store, child)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = runWorkerInvocation(ctx, w, "ORIGINAL_TASK: run the regression", nil)
	out, err := (&SendMessageTool{Workers: reg}).execute(context.Background(), json.RawMessage(`{"to":"`+w.ID+`","message":"please retry"}`))
	if err != nil || out.Err != nil {
		t.Fatalf("retry failed: %v / %v", err, out.Err)
	}
	if len(child.reqs) == 0 || !requestContains(child.reqs[0], "ORIGINAL_TASK: run the regression") || !requestContains(child.reqs[0], "please retry") {
		t.Fatal("retry lost the original task")
	}
	if s := w.Snapshot(); s.Status != "done" || s.Runs != 1 {
		t.Fatalf("retried worker = %q runs %d", s.Status, s.Runs)
	}
}

// Start failure of a background worker (its run is refused before any model
// event): definite failed state with the reason, saved, not counted as running
// in the background, and the coordinator is notified (wake + report).
func TestBackgroundWorkerStartFailureNotifiesCoordinator(t *testing.T) {
	reg := tools.NewRegistry()
	parent, err := NewLoop(LoopConfig{Provider: makeScriptedProvider("ok"), Registry: reg, Writer: &sessionWriterWithID{id: "sess-1"}})
	if err != nil {
		t.Fatal(err)
	}
	parent.Messages = append(parent.Messages, llm.Message{Role: llm.RoleUser, Content: "start"})
	specs := NewSubAgentRegistry()
	MustRegisterAll(specs, BuiltinSubAgents())
	task, err := NewAgentTool(specs, parent, reg, makeScriptedProvider("never"), nil, func(cfg LoopConfig) (*Loop, error) {
		l, err := NewLoop(cfg)
		if err == nil {
			l.sessionBusy.Store(true) // Run refuses: the worker cannot start
		}
		return l, err
	})
	if err != nil {
		t.Fatal(err)
	}
	store := newMemWorkerStore()
	task.Workers.SetStore(store, nil)
	ext := make(chan Event, 64)
	parent.SetExternalSink(ext)
	parent.SetBackgroundWake(WakeConfig{Debounce: testWakeDebounce})
	result, err := task.execute(context.Background(), json.RawMessage(`{"agent":"code","prompt":"run the regression","async":true}`))
	if err != nil || result.Err != nil {
		t.Fatalf("%v %v", err, result.Err)
	}
	if _, ok := waitWakeEvent(t, ext, 5*time.Second); !ok {
		t.Fatal("start failure did not wake the coordinator")
	}
	w, ok := task.Workers.Get("worker-1")
	if !ok {
		t.Fatal("worker missing")
	}
	if s := w.Snapshot(); s.Status != "failed" || s.LastError == "" {
		t.Fatalf("start failure left status %q (%q)", s.Status, s.LastError)
	}
	if n := parent.ActiveBackgroundWorkers(); n != 0 {
		t.Fatalf("failed worker still counted in background: %d", n)
	}
	rec, _, _ := store.LoadWorker(context.Background(), "sess-1", "worker-1")
	if rec.Status != "failed" {
		t.Fatalf("saved status = %q", rec.Status)
	}
	found := false
	for _, m := range parent.Messages {
		if m.Name == llm.WorkerNotificationName && strings.Contains(m.Content, "worker-1") &&
			(strings.Contains(m.Content, "<status>failed</status>") || strings.Contains(m.Content, "status=failed")) {
			found = true
		}
	}
	if !found {
		for _, m := range parent.Messages {
			t.Logf("%s %s %.300q", m.Role, m.Name, m.Content)
		}
		t.Fatal("coordinator got no failure report")
	}
}

// A worker spawned by an automatic wake-up turn runs, finishes and wakes the
// coordinator again (it is not orphaned when the wake turn ends).
func TestWorkerSpawnedDuringWakeTurnCompletes(t *testing.T) {
	reg := tools.NewRegistry()
	coordinator := makeScriptedProvider("unused")
	coordinator.scripts = [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "first", Name: "task", Arguments: `{"prompt":"FIRST_JOB","async":true}`}, FinishReason: "tool_calls"}},
		{{Content: "Started the first job.", FinishReason: "stop"}},
		{{ToolCall: &llm.ToolCall{ID: "second", Name: "task", Arguments: `{"prompt":"SECOND_JOB","async":true}`}, FinishReason: "tool_calls"}},
		{{Content: "Started the second job from the wake turn.", FinishReason: "stop"}},
		{{Content: "All done.", FinishReason: "stop"}},
	}
	parent, err := NewLoop(LoopConfig{Provider: coordinator, Registry: reg, Writer: &recordingWriter{}, MaxSteps: 6})
	if err != nil {
		t.Fatal(err)
	}
	child := makeScriptedProvider("JOB_REPORT")
	var childCalls atomic.Int32
	child.onCalled = func(int) { childCalls.Add(1) }
	specs := NewSubAgentRegistry()
	MustRegisterAll(specs, BuiltinSubAgents())
	task, err := NewAgentTool(specs, parent, reg, child, nil, NewLoop)
	if err != nil {
		t.Fatal(err)
	}
	reg.MustRegister(task.Spec())
	reg.MarkAlwaysOn("task")
	ext := make(chan Event, 128)
	parent.SetExternalSink(ext)
	parent.SetBackgroundWake(WakeConfig{Debounce: testWakeDebounce, Delegate: true}) // unattended delegation opted in
	events, err := parent.Run(context.Background(), "Delegate.")
	if err != nil {
		t.Fatal(err)
	}
	if !drainRun(t, events) {
		t.Fatal("first turn failed")
	}
	if _, ok := waitWakeEvent(t, ext, 5*time.Second); !ok {
		t.Fatal("first worker did not wake the coordinator")
	}
	wakeEvents, err := parent.RunWake(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !drainRun(t, wakeEvents) {
		t.Fatal("wake turn failed")
	}
	if _, ok := waitWakeEvent(t, ext, 5*time.Second); !ok {
		t.Fatal("worker spawned in the wake turn never reported")
	}
	w, ok := task.Workers.Get("worker-2")
	if !ok {
		t.Fatal("wake-turn worker missing")
	}
	if s := w.Snapshot(); s.Status != "done" || s.Runs != 1 {
		t.Fatalf("wake-turn worker = %q runs %d", s.Status, s.Runs)
	}
	if childCalls.Load() < 2 {
		t.Fatalf("wake-turn worker made no model call (%d)", childCalls.Load())
	}
}
