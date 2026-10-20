package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestLifecycleFailedHandoffDuringContinuation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cause := errors.New("original stream failed")
	p := &stubProvider{name: "offline-lifecycle", scripts: [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "observe", Name: "observe", Arguments: "{}"}}, {FinishReason: "tool_calls"}},
		{{Content: "Original partial report."}, {Err: cause}},
	}}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "observe", Description: "Return offline lifecycle evidence", ReadOnly: true, Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		return tools.Result{Text: "ORIGINAL_TOOL_EVIDENCE"}, nil
	}})
	workers := NewWorkerRegistry()
	w := workers.Add("general", "original", makeLoop(t, p, reg, ""))
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	defer finish()
	w.progress = func(ev WorkerProgressEvent) {
		if ev.Kind == "started" && ev.Run == 2 {
			close(entered)
			<-release
		}
	}
	handoff, err := runWorkerInvocation(ctx, w, "original", nil)
	if !errors.Is(err, cause) || !strings.Contains(handoff.evidence, "ORIGINAL_TOOL_EVIDENCE") {
		t.Fatalf("failed invocation was not finalized: err=%v evidence=%q", err, handoff.evidence)
	}
	want := handoff.result(w, err)
	done := make(chan error, 1)
	go func() {
		_, err := runWorkerInvocation(ctx, w, "continue", workers)
		done <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("continuation did not start")
	}
	// Exercise both error formatting and external UI delivery while the mutable
	// worker belongs to another invocation. Neither may change its cancel handle.
	for i := 0; i < 10; i++ {
		got := handoff.result(w, err)
		assertHandoffResultsEqual(t, got, want, cause)
		if !errors.Is(got.Err, cause) || !strings.Contains(got.Err.Error(), "status=failed") || !strings.Contains(got.Err.Error(), "ORIGINAL_TOOL_EVIDENCE") {
			t.Fatalf("failed-check/evidence recovery changed: %v", got.Err)
		}
	}
	events := make(chan Event, 1)
	parent := &Loop{}
	parent.SetExternalSink(events)
	(&AgentTool{ParentLoop: parent}).emitPreparedWorkerNotification(handoff)
	notice := (<-events).(WorkerNotificationEvent)
	if notice.Status != "failed" || notice.Text != handoff.notification || w.Snapshot().Runs != 2 || w.status() != "running" {
		t.Fatalf("UI snapshot reused continuation state: %+v", notice)
	}
	if err := workers.Stop(w.ID); err != nil {
		t.Fatal("old handoff interfered with new cancellation:", err)
	}
	finish()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "stopped by request") || w.status() != "stopped" || w.Stop() || p.calls != 2 {
			t.Fatalf("continuation stop/cleanup: err=%v status=%s requests=%d", err, w.status(), p.calls)
		}
	case <-ctx.Done():
		t.Fatal("stopped continuation did not finish")
	}
}

func TestLifecycleHandoffSurvivesEviction(t *testing.T) {
	p := &stubProvider{name: "offline-lifecycle", scripts: [][]llm.Delta{{{Content: "original report", FinishReason: "stop"}}}}
	workers := NewWorkerRegistry()
	workers.retention = 1
	w := workers.Add("general", "original", makeLoop(t, p, tools.NewRegistry(), ""))
	handoff, err := runWorkerInvocation(context.Background(), w, "original", nil)
	if err != nil {
		t.Fatal(err)
	}
	recent := workers.Add("general", "recent", nil)
	markFinished(recent, "done", "recent", "", time.Now().Add(time.Hour))
	workers.Add("general", "sweep", nil)
	if _, ok := workers.Evicted(w.ID); !ok {
		t.Fatal("original worker was not evicted")
	}
	if _, err := runWorkerInvocation(context.Background(), w, "stale continuation", workers); err == nil || !strings.Contains(err.Error(), "evicted") {
		t.Fatal("stale continuation admitted:", err)
	}
	result := handoff.result(w, nil)
	if handoff.snapshot.Runs != 1 || handoff.snapshot.Status != "done" || !strings.Contains(result.Text, "original report") || p.calls != 1 || w.Stop() {
		t.Fatalf("eviction corrupted handoff or leaked cancellation: %+v requests=%d", result, p.calls)
	}
}

func TestLifecycleEarlyExitCancelCleanup(t *testing.T) {
	t.Run("nil loop", func(t *testing.T) {
		w := NewWorkerRegistry().Add("general", "nil loop", nil)
		handoff, err := runWorkerInvocation(context.Background(), w, "run", nil)
		if err == nil || handoff.snapshot.Status != "failed" || w.Stop() || !w.runMu.TryLock() {
			t.Fatalf("nil-loop cleanup: err=%v status=%s", err, w.status())
		}
		w.runMu.Unlock()
	})
	t.Run("canceled before admission", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		w := NewWorkerRegistry().Add("general", "canceled", nil)
		handoff, err := runWorkerInvocation(ctx, w, "run", nil)
		if !errors.Is(err, context.Canceled) || handoff.snapshot.Runs != 0 || w.Stop() {
			t.Fatalf("pre-admission cleanup: err=%v snapshot=%+v", err, handoff.snapshot)
		}
	})
	t.Run("capacity refusal", func(t *testing.T) {
		workers := NewWorkerRegistry()
		workers.maxActive = 1
		w := workers.Add("general", "finished", nil)
		markFinished(w, "done", "old", "", time.Now())
		workers.Add("general", "active", nil)
		handoff, err := runWorkerInvocation(context.Background(), w, "run", workers)
		if err == nil || !strings.Contains(err.Error(), "worker limit reached") || handoff.snapshot.Status != "done" || handoff.snapshot.Runs != 0 || w.Stop() {
			t.Fatalf("refused admission cleanup: err=%v snapshot=%+v", err, handoff.snapshot)
		}
	})
	t.Run("started panic then continuation", func(t *testing.T) {
		p := &stubProvider{name: "offline-lifecycle", scripts: [][]llm.Delta{{{Content: "recovered", FinishReason: "stop"}}}}
		workers := NewWorkerRegistry()
		w := workers.Add("general", "panic", makeLoop(t, p, tools.NewRegistry(), ""))
		w.progress = func(ev WorkerProgressEvent) {
			if ev.Kind == "started" && ev.Run == 1 {
				panic("started callback panic")
			}
		}
		func() {
			defer func() {
				if got := recover(); got != "started callback panic" {
					t.Fatalf("panic not preserved: %v", got)
				}
			}()
			_, _ = runWorkerInvocation(context.Background(), w, "panic", nil)
		}()
		if w.status() != "failed" || w.Stop() || workers.Counts().Running != 0 || p.calls != 0 {
			t.Fatal("panic leaked cancellation or admission slot")
		}
		handoff, err := runWorkerInvocation(context.Background(), w, "continue", workers)
		if err != nil || handoff.snapshot.Status != "done" || handoff.snapshot.Runs != 2 || handoff.report != "recovered" || p.calls != 1 || w.Stop() {
			t.Fatalf("panic prevented continuation: err=%v snapshot=%+v", err, handoff.snapshot)
		}
	})
}
