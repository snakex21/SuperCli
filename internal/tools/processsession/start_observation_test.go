package processsession

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDefaultStartReportsImmediateExitAndDiagnostics(t *testing.T) {
	for _, mode := range []string{"echo", "fail", "delayed-fail"} {
		t.Run(mode, func(t *testing.T) {
			tool := New(t.TempDir())
			t.Cleanup(tool.Close)
			raw, _ := json.Marshal(map[string]any{"action": "start", "command": helperCommand(mode), "env": []string{"SUPERCLI_PROCESS_HELPER=1"}})
			result, err := tool.Execute(context.Background(), raw)
			if err != nil {
				t.Fatal(err)
			}
			var snap snapshot
			if err := json.Unmarshal([]byte(result.Text), &snap); err != nil {
				t.Fatal(err)
			}
			wantCode := 7
			if mode == "echo" {
				wantCode = 0
			}
			if snap.Status == "running" || snap.ExitCode == nil || *snap.ExitCode != wantCode {
				t.Fatalf("early exit not observed by start: %+v", snap)
			}
			if wantCode != 0 && (result.Err == nil || !strings.Contains(result.ModelContent(), "exit=7") || !strings.Contains(result.ModelContent(), "specific failing assertion")) {
				t.Fatalf("start lost failure evidence: %+v", result)
			}
			if wantCode == 0 && result.Err != nil {
				t.Fatalf("successful early exit reported as failure: %v", result.Err)
			}
		})
	}
}

func TestStartObservationCancellationPreservesProcess(t *testing.T) {
	tool := New(t.TempDir())
	t.Cleanup(tool.Close)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := &waitObservedContext{Context: ctx, entered: make(chan struct{})}
	results := make(chan struct {
		snap snapshot
		err  error
	}, 1)
	go func() {
		snap, err := tool.Manager.start(observed, params{Command: helperCommand("stdin"), Env: []string{"SUPERCLI_PROCESS_HELPER=1"}})
		results <- struct {
			snap snapshot
			err  error
		}{snap, err}
	}()
	select {
	case <-observed.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("start wait was not entered")
	}
	cancel()
	var start snapshot
	select {
	case result := <-results:
		if !errors.Is(result.err, context.Canceled) || result.snap.Status != "running" || result.snap.ID == "" {
			t.Fatalf("cancel lost persistent process: %+v", result)
		}
		start = result.snap
	case <-time.After(5 * time.Second):
		t.Fatal("caller cancellation did not release startup wait")
	}
	if _, err := tool.Manager.Write(start.ID, "still usable", true); err != nil {
		t.Fatal(err)
	}
	final, err := tool.Manager.Wait(context.Background(), start.ID)
	if err != nil || final.Status != "done" || !strings.Contains(final.Stdout, "still usable") {
		t.Fatalf("cancelled wait stopped process or lost output: %+v %v", final, err)
	}
}

func TestCancelledStartDoesNotLaunch(t *testing.T) {
	tool := New(t.TempDir())
	t.Cleanup(tool.Close)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := tool.Manager.start(ctx, params{Command: []string{os.Args[0]}})
	if !errors.Is(err, context.Canceled) || len(tool.Manager.items) != 0 {
		t.Fatalf("pre-cancelled start launched a process: %v", err)
	}
}

func TestStartupObservationCanBeSkipped(t *testing.T) {
	tool := New(t.TempDir())
	t.Cleanup(tool.Close)
	// Cancel when the startup wait tries to select on Done. With explicit
	// yield_ms:0 it never enters that wait; no wall-clock latency assertion.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := &waitObservedContext{Context: ctx, entered: make(chan struct{})}
	snap, err := tool.Manager.start(observed, params{Command: helperCommand("stdin"), Env: []string{"SUPERCLI_PROCESS_HELPER=1"}, YieldMS: new(int)})
	if err != nil || snap.Status != "running" {
		t.Fatalf("zero-yield launch failed: %+v %v", snap, err)
	}
	select {
	case <-observed.entered:
		t.Fatal("zero-yield start entered an observation wait")
	default:
	}
}

func TestStoppedProcessRetainsExplicitHistoricalReadiness(t *testing.T) {
	tool := New(t.TempDir())
	t.Cleanup(tool.Close)
	start, err := tool.Manager.Start(params{Command: helperCommand("ready"), Env: []string{"SUPERCLI_PROCESS_HELPER=1"}, YieldMS: new(int)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Manager.Write(start.ID, "go", true); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ready, err := tool.Manager.WaitReady(ctx, start.ID, "READY zażółć", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	stopped, err := tool.Manager.Stop(ctx, start.ID)
	if err != nil || stopped.Ready || stopped.ReadyAt != ready.ReadyAt || !strings.Contains(stopped.StatusSummary, "Past readiness observation") || !strings.Contains(stopped.StatusSummary, "no longer running") {
		t.Fatalf("historical timestamp was misleading: %+v %v", stopped, err)
	}
	listed := tool.Manager.List()
	if len(listed) != 1 || listed[0].Ready || listed[0].ReadyAt != ready.ReadyAt || !strings.Contains(listed[0].StatusSummary, "Past readiness observation") {
		t.Fatalf("list lost historical meaning: %+v", listed)
	}
	stopped.Stdout = strings.Repeat("output\n", 2000)
	var preview snapshot
	if err := json.Unmarshal([]byte(stopped.modelPreview()), &preview); err != nil || preview.Ready || preview.ReadyAt != ready.ReadyAt || !strings.Contains(preview.StatusSummary, "Past readiness observation") {
		t.Fatalf("preview lost historical meaning: %+v %v", preview, err)
	}
}

func TestExitedProcessFailureVisibleWhileOutputDrains(t *testing.T) {
	tool, item, _ := waitFixture(t)
	for _, code := range []int{0, 7} {
		item.mu.Lock()
		item.exitCode = code
		item.ended = time.Now()
		item.mu.Unlock()
		item.exited.Store(true)
		snap := item.snapshot(maxBufferBytes)
		if snap.Status != "exited_draining" || snap.Ready || snap.ExitCode == nil || *snap.ExitCode != code || !snap.OutputPending || !strings.Contains(snap.OutputWarning, "Use wait") {
			t.Fatalf("native exit was still presented as running: %+v", snap)
		}
		if (snap.commandFailure() != nil) != (code != 0) {
			t.Fatalf("draining changed command outcome: %+v", snap)
		}
		listed := tool.Manager.List()
		if len(listed) != 1 || listed[0].Status != "exited_draining" || !listed[0].OutputPending {
			t.Fatalf("list lost pending output state: %+v", listed)
		}
		var preview snapshot
		if err := json.Unmarshal([]byte(snap.modelPreview()), &preview); err != nil || !preview.OutputPending || preview.ExitCode == nil || *preview.ExitCode != code {
			t.Fatalf("preview lost pending output state: %+v %v", preview, err)
		}
	}
}
