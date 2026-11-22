package agent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
)

// briefStore is a memWorkerStore that also lists briefs without transcripts.
type briefStore struct{ *memWorkerStore }

func (b briefStore) ListWorkerBriefs(ctx context.Context, sid string) ([]WorkerRecord, error) {
	recs, err := b.ListWorkers(ctx, sid)
	for i := range recs {
		recs[i].Messages = nil
	}
	return recs, err
}

// An automatic turn cannot route around the resume guard by starting a new
// task with a failed worker's brief - live or known only from the database.
func TestWakeTurnRefusesRepeatingAWorkersTask(t *testing.T) {
	f, _ := contractFixture(t, true,
		[][]llm.Delta{
			callStep(tcall("t1", "task", `{"prompt":"add the spider"}`)), say("Delegated."),
			// automatic turn: same brief as the failed worker, then the brief
			// of a worker known only from the database
			callStep(tcall("t2", "task", `{"prompt":"add the spider"}`), tcall("t3", "task", `{"prompt":"add the creeper"}`)),
			say("Both earlier tasks need your decision."),
		},
		[][]llm.Delta{say("partial")})
	f.parent.writer = &sessionWriterWithID{id: "sess-repeat"}
	store := briefStore{newMemWorkerStore()}
	f.task.Workers.SetStore(store, []string{"worker-90"})
	runTurnWithin(t, f.parent, "Dodaj pająka.", 3*time.Second)
	w := f.task.Workers.List()[0]
	waitWorker(t, w, "first run", func(s Snapshot) bool { return s.Status == "done" })
	w.setState(func(w *Worker) { w.Status, w.LastError = "failed", "stream error" })
	_ = store.SaveWorker(context.Background(), WorkerRecord{SessionID: sessionIDOf(f.parent), ID: "worker-90", Agent: "general", Description: "add the creeper", Status: WorkerStatusInterrupted, Runs: 1})
	childCalls := atomic.LoadInt32(&f.child.calls)
	f.parent.DeliverWorkerReport(context.Background(), "<task-notification><task-id>worker-1</task-id><status>failed</status></task-notification>")
	events, err := f.parent.RunWake(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	refused := 0
	for ev := range events {
		if r, ok := ev.(ToolResultEvent); ok && r.Err != nil && strings.Contains(r.Err.Error(), "would repeat") {
			refused++
		}
	}
	if refused != 2 {
		t.Fatalf("refused repeats = %d, want 2", refused)
	}
	if n := len(f.task.Workers.List()); n != 1 || atomic.LoadInt32(&f.child.calls) != childCalls {
		t.Fatalf("an automatic turn started work again: workers=%d", n)
	}
}
