package processsession

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestNativeProcessReadinessWaitThenExit(t *testing.T) {
	tool := New(t.TempDir())
	defer tool.Close()
	start, err := tool.Manager.Start(params{Command: helperCommand("ready"), Env: []string{"SUPERCLI_PROCESS_HELPER=1"}, YieldMS: new(int)})
	if err != nil || start.Ready || !strings.Contains(start.StatusSummary, "not been verified") {
		t.Fatalf("start: %+v %v", start, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	observed := &waitObservedContext{Context: ctx, entered: make(chan struct{})}
	results := make(chan snapshot, 1)
	go func() {
		snap, err := tool.Manager.WaitReady(observed, start.ID, "READY zażółć", 0, 0)
		if err != nil {
			t.Error(err)
		}
		results <- snap
	}()
	select {
	case <-observed.entered:
	case <-ctx.Done():
		t.Fatal("wait not entered")
	}
	if _, err := tool.Manager.Write(start.ID, "go", true); err != nil {
		t.Fatal(err)
	}
	select {
	case snap := <-results:
		if !snap.Ready {
			t.Fatalf("not ready: %+v", snap)
		}
	case <-ctx.Done():
		t.Fatal("ready log did not release wait")
	}
	if _, err := tool.Manager.Write(start.ID, "exit", true); err != nil {
		t.Fatal(err)
	}
	final, err := tool.Manager.Wait(ctx, start.ID)
	if err != nil || final.Ready || final.Status != "done" || !strings.Contains(final.StatusSummary, "code 0 after") {
		t.Fatalf("final: %+v %v", final, err)
	}
}
