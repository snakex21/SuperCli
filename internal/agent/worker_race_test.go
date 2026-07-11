package agent

import (
	"context"
	"sync"
	"testing"
)

// TestWorker_SnapshotDuringRun_NoRace drives runWorkerLoop (which mutates
// Status, UpdatedAt, LastError, TokensIn/Out, LastResult) concurrently with
// Snapshot() and Counts() — the exact TUI / "/workers" read paths. Run with
// -race: any unguarded state access shows up here.
func TestWorker_SnapshotDuringRun_NoRace(t *testing.T) {
	loop, err := NewLoop(LoopConfig{
		Provider: &stubReplyProvider{name: "test", reply: "ok"},
		Registry: newTestBaseRegistry(),
		MaxSteps: 1,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	r := NewWorkerRegistry()
	w := r.Add("general", "race probe", loop)

	const runs = 25
	var wg sync.WaitGroup
	done := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(done)
		for i := 0; i < runs; i++ {
			// Each call writes Status/UpdatedAt/LastError at start and
			// Status/UpdatedAt/LastResult/TokensIn/Out at end.
			_, _ = runWorkerLoop(context.Background(), w, "go")
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			s := w.Snapshot()
			_ = s.Status
			_ = r.Counts()
		}
	}()

	wg.Wait()
}
