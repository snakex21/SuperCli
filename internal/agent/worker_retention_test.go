package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// markFinished flips a worker to a terminal status with a given
// UpdatedAt, the way runWorkerLoop does at the end of a run.
func markFinished(w *Worker, status, result, errText string, at time.Time) {
	w.setState(func(w *Worker) {
		w.Status = status
		w.LastResult = result
		w.LastError = errText
		w.UpdatedAt = at
		w.TokensIn += 11
		w.TokensOut += 7
		w.Steps++
	})
}

// TestWorkerRegistry_EvictionLRU: over-retention finished workers are
// evicted oldest-first (by UpdatedAt), a compact summary survives, and
// ACTIVE workers are never evicted no matter how old they are.
func TestWorkerRegistry_EvictionLRU(t *testing.T) {
	r := NewWorkerRegistry()
	r.retention = 1

	base := time.Now().Add(-time.Hour)
	oldDone := r.Add("general", "oldest finished task", nil)
	markFinished(oldDone, "done", "wrote the summary file", "", base)

	// Active and OLDER than everything else — must survive eviction.
	activeOld := r.Add("general", "long-running task", nil)
	activeOld.setState(func(w *Worker) {
		w.Status = "running"
		w.UpdatedAt = base.Add(-time.Hour)
	})

	newDone := r.Add("general", "newest finished task", nil)
	markFinished(newDone, "failed", "partial output", "boom", base.Add(time.Minute))

	// Adding a fresh worker triggers the retention sweep:
	// finished = {oldDone, newDone} > retention 1 → oldDone goes.
	r.Add("general", "trigger", nil)

	if _, ok := r.Get(oldDone.ID); ok {
		t.Fatalf("oldest finished worker %s should have been evicted", oldDone.ID)
	}
	if _, ok := r.Get(activeOld.ID); !ok {
		t.Fatalf("active worker %s must never be evicted", activeOld.ID)
	}
	if _, ok := r.Get(newDone.ID); !ok {
		t.Fatalf("newest finished worker %s is within retention and must stay", newDone.ID)
	}

	e, ok := r.Evicted(oldDone.ID)
	if !ok {
		t.Fatalf("evicted worker %s must keep a summary", oldDone.ID)
	}
	if e.Status != "done" || e.Agent != "general" {
		t.Errorf("summary status/agent = %q/%q, want done/general", e.Status, e.Agent)
	}
	if !strings.Contains(e.LastResult, "wrote the summary file") {
		t.Errorf("summary must keep the (capped) LastResult, got %q", e.LastResult)
	}
	if e.TokensIn != 11 || e.TokensOut != 7 || e.Steps != 1 {
		t.Errorf("summary tokens/steps = %d/%d/%d, want 11/7/1", e.TokensIn, e.TokensOut, e.Steps)
	}
	if line := e.Line(); !strings.Contains(line, oldDone.ID) || !strings.Contains(line, "done") {
		t.Errorf("Line() should identify the worker and status, got %q", line)
	}

	// Counts still sees the evicted worker in Total and the done bucket.
	c := r.Counts()
	// Running 2 = the old active worker + the freshly created trigger.
	if c.Total != 4 || c.Done != 1 || c.Failed != 1 || c.Running != 2 {
		t.Errorf("counts = %+v, want Total 4, Done 1, Failed 1, Running 2", c)
	}
}

// TestSendMessage_EvictedWorker: send_message to an evicted worker
// returns a clear error carrying the kept summary, not "unknown".
func TestSendMessage_EvictedWorker(t *testing.T) {
	r := NewWorkerRegistry()
	r.retention = 1
	old := r.Add("general", "task one", nil)
	markFinished(old, "done", "final report text", "", time.Now().Add(-time.Hour))
	other := r.Add("general", "task two", nil)
	markFinished(other, "done", "", "", time.Now())
	r.Add("general", "trigger", nil)

	tool := NewSendMessageTool(r)
	raw, _ := json.Marshal(map[string]string{"to": old.ID, "message": "continue"})
	res, err := tool.execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("execute returned transport error: %v", err)
	}
	if res.Err == nil {
		t.Fatal("send_message to an evicted worker must fail")
	}
	msg := res.Err.Error()
	if !strings.Contains(msg, "evicted") {
		t.Errorf("error should say the worker was evicted, got %q", msg)
	}
	if !strings.Contains(msg, "final report text") {
		t.Errorf("error should carry the kept summary, got %q", msg)
	}

	// A genuinely unknown id keeps the historical error shape.
	raw, _ = json.Marshal(map[string]string{"to": "worker-999", "message": "hi"})
	res, _ = tool.execute(context.Background(), raw)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "unknown worker") {
		t.Errorf("unknown id should stay 'unknown worker', got %v", res.Err)
	}
}

// TestWorkerRegistry_StopEvicted: task_stop / "/workers stop" on an
// evicted id explains the eviction instead of "unknown".
func TestWorkerRegistry_StopEvicted(t *testing.T) {
	r := NewWorkerRegistry()
	r.retention = 1
	old := r.Add("general", "task", nil)
	markFinished(old, "stopped", "", "stopped by request", time.Now().Add(-time.Hour))
	next := r.Add("general", "task", nil)
	markFinished(next, "done", "", "", time.Now())
	r.Add("general", "trigger", nil)

	err := r.Stop(old.ID)
	if err == nil || !strings.Contains(err.Error(), "evicted") {
		t.Errorf("Stop on evicted id should explain the eviction, got %v", err)
	}
}

// TestWorkerRegistry_ActiveLimit: TryAdd fails fast once maxActive
// workers are active, and opens up again when one finishes. Finished
// workers never count against the cap. Add() stays uncapped.
func TestWorkerRegistry_ActiveLimit(t *testing.T) {
	r := NewWorkerRegistry()
	r.maxActive = 2

	w1, err := r.TryAdd("general", "one", nil)
	if err != nil {
		t.Fatalf("first TryAdd: %v", err)
	}
	if _, err = r.TryAdd("general", "two", nil); err != nil {
		t.Fatalf("second TryAdd: %v", err)
	}

	_, err = r.TryAdd("general", "three", nil)
	if err == nil {
		t.Fatal("third TryAdd must hit the active-worker limit")
	}
	if !strings.Contains(err.Error(), "worker limit reached") {
		t.Errorf("limit error should be self-explanatory, got %q", err.Error())
	}

	// A finished worker frees its slot.
	markFinished(w1, "done", "", "", time.Now())
	if _, err = r.TryAdd("general", "three again", nil); err != nil {
		t.Errorf("TryAdd after a worker finished: %v", err)
	}

	// Add (test/legacy path) is deliberately uncapped.
	if w := r.Add("general", "uncapped", nil); w == nil {
		t.Error("Add must stay uncapped")
	}
}

// TestEnvPositiveInt pins the override parsing: unset/garbage/zero
// fall back to the default; a positive integer wins.
func TestEnvPositiveInt(t *testing.T) {
	const key = "SUPERCLI_TEST_POSITIVE_INT"
	t.Setenv(key, "")
	if got := envPositiveInt(key, 20); got != 20 {
		t.Errorf("empty = %d, want default 20", got)
	}
	t.Setenv(key, "abc")
	if got := envPositiveInt(key, 20); got != 20 {
		t.Errorf("garbage = %d, want default 20", got)
	}
	t.Setenv(key, "0")
	if got := envPositiveInt(key, 20); got != 20 {
		t.Errorf("zero = %d, want default 20", got)
	}
	t.Setenv(key, "50")
	if got := envPositiveInt(key, 20); got != 50 {
		t.Errorf("override = %d, want 50", got)
	}
}
