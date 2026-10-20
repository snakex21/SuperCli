package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// Deliver the first invocation only after a continuation has entered its fake
// provider. Report metadata and evidence must still belong to invocation one.
func TestLifecycleHandoffAfterContinuationStarts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	release := make(chan struct{}, 1)
	release <- struct{}{}
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	defer finish()
	p := &continuationLimitProvider{entered: make(chan struct{}, 2), release: release}
	workers := NewWorkerRegistry()
	w := workers.Add("general", "first invocation", makeLoop(t, p, tools.NewRegistry(), ""))
	// Seed the same 25-byte observation at the finished-event boundary, before
	// the invocation is captured under runMu.
	w.progress = func(ev WorkerProgressEvent) {
		if ev.Kind == "finished" && w.Snapshot().Runs == 1 {
			w.setState(func(w *Worker) { w.lastEvidence = "FIRST_INVOCATION_EVIDENCE" })
		}
	}
	before, err := runWorkerInvocation(ctx, w, "first invocation", nil)
	if err != nil {
		t.Fatal(err)
	}
	<-p.entered
	text := before.report
	if before.snapshot.Runs != 1 || before.snapshot.Status != "done" || before.evidence == "" {
		t.Fatal("invalid first invocation fixture")
	}
	done := make(chan error, 1)
	go func() {
		_, err := runWorkerLoopInRegistry(ctx, w, "follow-up", workers)
		done <- err
	}()
	select {
	case <-p.entered:
	case <-ctx.Done():
		t.Fatal("continuation did not enter fake provider")
	}
	// This is where task/send_message/background callers deliver after return.
	late := before
	result := late.result(w, nil)
	if late.snapshot.Runs != 1 || late.snapshot.Status != "done" || len(late.evidence) != 25 || !strings.Contains(result.Text, "<status>done</status>") || !strings.Contains(result.Text, "FIRST_INVOCATION_EVIDENCE") || !strings.Contains(result.Text, text) {
		t.Fatalf("handoff reused continuation state: run=%d status=%s evidence=%q result=%q", late.snapshot.Runs, late.snapshot.Status, late.evidence, result.Text)
	}
	finish()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("continuation failed to finish")
	}
	t.Logf("captured: completed run=%d/%s; delayed handoff=%d/%s; original evidence bytes=%d -> %d; fake requests=%d", before.snapshot.Runs, before.snapshot.Status, late.snapshot.Runs, late.snapshot.Status, len(before.evidence), len(late.evidence), p.calls.Load())
}

// Stop must already be installed at the published-running started boundary.
func TestLifecycleStopAtStartedEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p := &stubProvider{name: "offline-lifecycle-fixture", scripts: [][]llm.Delta{{{Content: "completed despite stop request", FinishReason: "stop"}}}}
	workers := NewWorkerRegistry()
	w := workers.Add("general", "stop boundary", makeLoop(t, p, tools.NewRegistry(), ""))
	var stopErr error
	var observed string
	w.progress = func(ev WorkerProgressEvent) {
		if ev.Kind == "started" {
			observed = w.status()
			stopErr = workers.Stop(w.ID)
		}
	}
	text, err := runWorkerLoop(ctx, w, "stop boundary")
	if err == nil || observed != "running" || stopErr != nil || w.status() != "stopped" || p.calls != 0 || text != "" || w.Stop() {
		t.Fatalf("started stop was lost or leaked: observed=%s stop=%v final=%s requests=%d report=%q err=%v", observed, stopErr, w.status(), p.calls, text, err)
	}
	t.Logf("stopped: observed=%s; stop=%v; final=%s; fake requests=%d", observed, stopErr, w.status(), p.calls)
}
