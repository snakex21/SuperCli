package agent

import (
	"context"
	"sync/atomic"
	"testing"

	"supercli/internal/llm"
)

// A worker started on model A that stopped on a usage limit resumes on the
// worker model the user picked since (B), with the current reasoning effort;
// the snapshot the card and agents panel read reports B.
func TestResumedWorkerUsesCurrentWorkerSettings(t *testing.T) {
	f, _ := contractFixture(t, false,
		[][]llm.Delta{
			callStep(tcall("t1", "task", `{"prompt":"spider"}`)), say("Delegated."),
			callStep(tcall("s1", "send_message", `{"to":"worker-1","message":"continue"}`)), say("Wznowiłem worker-1."),
		},
		[][]llm.Delta{say("spider partial")})
	f.task.WorkerReasoningEffort = "high"
	runTurnText(t, f.parent, "Dodaj pająka.")
	w := f.task.Workers.List()[0]
	if got := workerRunModel(w); got != "openai-worker" {
		t.Fatalf("setup: worker model = %q", got)
	}
	w.setState(func(w *Worker) { w.Status, w.LastError = "failed", usageLimitError })

	// The user switches the worker model and reasoning in the side panel.
	b := &stubProvider{name: "codex-gpt-b", scripts: [][]llm.Delta{say("spider done on B")}}
	f.task.WorkerProvider = b
	f.task.WorkerReasoningEffort = "low"
	childCalls := atomic.LoadInt32(&f.child.calls)

	runTurnText(t, f.parent, "limit wrócił, wznów go")

	if got := atomic.LoadInt32(&b.calls); got != 1 {
		t.Fatalf("resumed run calls on B = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&f.child.calls); got != childCalls {
		t.Fatalf("resumed run still called the old model A (%d calls)", got-childCalls)
	}
	if got := workerRunModel(w); got != "codex-gpt-b" {
		t.Fatalf("worker model after resume = %q, want codex-gpt-b", got)
	}
	w.stateMu.RLock()
	effort := w.reasoningEffort
	w.stateMu.RUnlock()
	if effort != "low" {
		t.Fatalf("worker reasoning after resume = %q, want low", effort)
	}
	if s := w.Snapshot(); s.WorkerModel != "codex-gpt-b" || s.Model != "codex-gpt-b" {
		t.Fatalf("card model = %q / %q, want codex-gpt-b", s.WorkerModel, s.Model)
	}
}

// A worker given another model earlier (C) still takes the user's newer
// worker setting (B) when it resumes.
func TestUsersNewerWorkerModelWinsOverEarlierWorkerModel(t *testing.T) {
	f, _ := contractFixture(t, false,
		[][]llm.Delta{callStep(tcall("t1", "task", `{"prompt":"spider"}`)), say("Delegated.")},
		[][]llm.Delta{say("spider partial")})
	runTurnText(t, f.parent, "Dodaj pająka.")
	w := f.task.Workers.List()[0]
	w.Loop.SetModel(&stubProvider{name: "explicit-c"})
	b := &stubProvider{name: "codex-gpt-b"}
	f.task.WorkerProvider = b
	if !refreshIfIdle(context.Background(), w, f.task.refreshWorkerSettingsLocked) {
		t.Fatal("idle worker was not refreshed")
	}
	if got := workerRunModel(w); got != "codex-gpt-b" {
		t.Fatalf("worker model = %q, want the user's newer choice codex-gpt-b", got)
	}
}

// A running worker is never swapped mid-run: the change waits for its next
// run.
func TestRunningWorkerKeepsItsModelUntilNextRun(t *testing.T) {
	f, _ := contractFixture(t, false,
		[][]llm.Delta{callStep(tcall("t1", "task", `{"prompt":"spider"}`)), say("Delegated.")},
		[][]llm.Delta{say("spider partial")})
	runTurnText(t, f.parent, "Dodaj pająka.")
	w := f.task.Workers.List()[0]
	f.task.WorkerProvider = &stubProvider{name: "codex-gpt-b"}
	f.task.WorkerReasoningEffort = "low"
	w.runMu.Lock() // the worker is in a run
	if refreshIfIdle(context.Background(), w, f.task.refreshWorkerSettingsLocked) {
		t.Fatal("a running worker was refreshed")
	}
	if got := workerRunModel(w); got != "openai-worker" {
		t.Fatalf("running worker model = %q, want openai-worker unchanged", got)
	}
	w.runMu.Unlock()
	if !refreshIfIdle(context.Background(), w, f.task.refreshWorkerSettingsLocked) || workerRunModel(w) != "codex-gpt-b" {
		t.Fatalf("next run model = %q, want codex-gpt-b", workerRunModel(w))
	}
}
