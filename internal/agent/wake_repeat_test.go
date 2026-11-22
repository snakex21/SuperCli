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

// With background_wake_delegate off an automatic turn starts no new workers at
// all. On (the default) it may hand a new planned task to a free slot, but a
// repeat of a failed, interrupted or stopped worker's task is still refused,
// also when reworded (matched by goal and task, not wording).
func TestWakeTurnStartsNoNewWorkers(t *testing.T) {
	for _, delegate := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[delegate], func(t *testing.T) {
			f, _ := contractFixture(t, true,
				[][]llm.Delta{
					callStep(tcall("t1", "task", `{"prompt":"add the spider"}`)), say("Delegated."),
					callStep(tcall("t2", "task", `{"prompt":"add the spider"}`), tcall("t3", "task", `{"prompt":"add the creeper"}`), tcall("t4", "task", `{"prompt":"please add a spider mob, retry"}`), tcall("t5", "task", `{"prompt":"write the README for the world generator"}`)),
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
			want := 4 // off: nothing starts
			if delegate {
				want = 3 // the repeats, reworded one included; only the new README task starts
			}
			if refused != want {
				t.Fatalf("refused = %d, want %d", refused, want)
			}
			if !delegate && (len(f.task.Workers.List()) != 1 || atomic.LoadInt32(&f.child.calls) != childCalls) {
				t.Fatalf("an automatic turn started work: workers=%d", len(f.task.Workers.List()))
			}
			if delegate && len(f.task.Workers.List()) != 2 {
				t.Fatalf("on: the new planned task should start: workers=%d", len(f.task.Workers.List()))
			}
		})
	}
}

func TestSameTaskMatchesRewordedRetries(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"add the spider", "please add a spider mob, retry", true},
		{"Dodaj pająka do gry", "Ponownie dodaj pająki do gry", true},
		{"CEL: naprawić generator świata\nKontekst: ...", "CEL: naprawić generator świata\nInny kontekst, inne słowa i kroki", true},
		{"add the spider", "write the README for the world generator", false},
		{"add the creeper", "add the spider", false},
		// Review: one template, different identifiers = different tasks.
		{"Implement unit tests for the parser module in internal/parser", "Implement unit tests for the lexer module in internal/lexer", false},
		{"Translate the UI strings to German (de.json)", "Translate the UI strings to French (fr.json)", false},
		{"Fix the failing TestLoginHandler test", "Fix the failing TestLogoutHandler test", false},
		{"Add an index to internal/storage/session", "Add an index to internal/storage/workers", false},
		{"Fix the failing TestLoginHandler test", "please retry: fix TestLoginHandler, it still fails", true},
	} {
		if got := sameTask(c.a, c.b); got != c.want {
			t.Errorf("sameTask(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
