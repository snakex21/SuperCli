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

// An automatic turn starts no new workers by default (review 3: matching
// briefs word for word was beaten by rephrasing): even a reworded task is
// refused. With background_wake_delegate the user allows unattended refills;
// then a repeat of an existing worker's task (live or saved) is still refused.
func TestWakeTurnStartsNoNewWorkers(t *testing.T) {
	for _, delegate := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "delegate"}[delegate], func(t *testing.T) {
			f, _ := contractFixture(t, true,
				[][]llm.Delta{
					callStep(tcall("t1", "task", `{"prompt":"add the spider"}`)), say("Delegated."),
					callStep(tcall("t2", "task", `{"prompt":"add the spider"}`), tcall("t3", "task", `{"prompt":"add the creeper"}`), tcall("t4", "task", `{"prompt":"please add a spider mob, retry"}`)),
					say("Reported."),
				},
				[][]llm.Delta{say("partial")})
			f.parent.SetBackgroundWake(WakeConfig{Debounce: testWakeDebounce, Delegate: delegate})
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
				if r, ok := ev.(ToolResultEvent); ok && r.Err != nil && strings.Contains(r.Err.Error(), "refused:") {
					refused++
				}
			}
			want := 3 // default: nothing starts
			if delegate {
				want = 2 // the two repeats; the reworded task is new work the user allowed
			}
			if refused != want {
				t.Fatalf("refused = %d, want %d", refused, want)
			}
			if !delegate && (len(f.task.Workers.List()) != 1 || atomic.LoadInt32(&f.child.calls) != childCalls) {
				t.Fatalf("an automatic turn started work: workers=%d", len(f.task.Workers.List()))
			}
		})
	}
}
