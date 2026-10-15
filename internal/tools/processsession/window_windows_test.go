//go:build windows

package processsession

import (
	"context"
	"encoding/json"
	"os"
	"sync/atomic"
	"testing"

	"supercli/internal/system/windowautomation"
	"supercli/internal/tools/core"
)

func windowFixture(t *testing.T) *Tool {
	t.Helper()
	tool := New(t.TempDir())
	tool.Manager.items["proc-owned"] = &process{id: "proc-owned", status: "running", pid: os.Getpid(), windowBirth: windowautomation.Birth(os.Getpid())}
	return tool
}

func TestExternalScreenshotRunsWithoutConfirmation(t *testing.T) {
	tool := windowFixture(t)
	calls := 0
	tool.captureScreenshot = func(_ context.Context, raw json.RawMessage) (core.Result, error) {
		var args map[string]any
		if err := json.Unmarshal(raw, &args); err != nil || args["window_id"] != "0x1234" || args["process_id"] != float64(os.Getpid()) {
			t.Fatal("external capture lost its exact process/window scope")
		}
		calls++
		return core.Result{Text: "fixture pixels"}, nil
	}
	raw, _ := json.Marshal(map[string]any{"action": "screenshot", "process_id": os.Getpid(), "window_id": "0x1234"})
	r, err := tool.Execute(context.Background(), raw)
	if err != nil || r.Err != nil || r.Text != "fixture pixels" || calls != 1 {
		t.Fatalf("external capture failed: %v %v", err, r.Err)
	}
}

func TestWindowDiscardsObservationAfterOwnershipEnds(t *testing.T) {
	for _, mode := range []string{"stop", "exit", "close", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			tool := windowFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tool.runWindow = func(context.Context, *windowautomation.Process, windowautomation.Request) (windowautomation.Result, error) {
				switch mode {
				case "stop":
					tool.Manager.items["proc-owned"].stopRequested = true
				case "exit":
					tool.Manager.items["proc-owned"].exited.Store(true)
				case "close":
					tool.Manager.closed = true
				case "cancel":
					cancel()
				}
				return windowautomation.Result{PID: os.Getpid(), Title: "must not be returned"}, nil
			}
			r, err := tool.Execute(ctx, json.RawMessage(`{"action":"inspect_window","id":"proc-owned"}`))
			if err != nil || r.Err == nil || r.Text != "" {
				t.Fatalf("ownership ended but observation returned: %+v %v", r, err)
			}
		})
	}
}

func TestWindowExternalRunsWithoutConfirmation(t *testing.T) {
	tool := windowFixture(t)
	calls := 0
	tool.runWindow = func(_ context.Context, p *windowautomation.Process, r windowautomation.Request) (windowautomation.Result, error) {
		calls++
		return windowautomation.Result{PID: p.PID}, nil
	}
	raw, _ := json.Marshal(map[string]any{"action": "inspect_window", "process_id": os.Getpid()})
	for i := 0; i < 3; i++ {
		r, err := tool.Execute(context.Background(), raw)
		if err != nil || r.Err != nil {
			t.Fatalf("call %d: %v %v", i, err, r.Err)
		}
	}
	if calls != 3 {
		t.Fatalf("calls=%d, want 3", calls)
	}
	// Physical-input flags are still rejected for external targets.
	r, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"click","process_id":1,"element_id":"el_x","use_mouse":true}`))
	if err != nil || r.Err == nil || calls != 3 {
		t.Fatal("use_mouse flag accepted")
	}
}

func TestWindowOwnedScopeAndCancellation(t *testing.T) {
	tool := windowFixture(t)
	var calls atomic.Int32
	tool.runWindow = func(_ context.Context, p *windowautomation.Process, r windowautomation.Request) (windowautomation.Result, error) {
		calls.Add(1)
		return windowautomation.Result{PID: p.PID}, nil
	}
	for _, raw := range []string{
		`{"action":"inspect_window","id":"unknown"}`,
		`{"action":"inspect_window","id":"proc-owned","process_id":1}`,
		`{"action":"inspect_window","id":"proc-owned","approved":true}`,
		`{"action":"click","id":"proc-owned","element_id":"el_x","use_mouse":true}`,
		`{"action":"type","id":"proc-owned","element_id":"el_x"}`,
		`{"action":"type","id":"proc-owned","element_id":"el_x","text":null}`,
		`{"action":"type","id":"proc-owned","element_id":"el_x","input":"wrong field"}`,
	} {
		r, err := tool.Execute(context.Background(), json.RawMessage(raw))
		if err != nil || r.Err == nil {
			t.Fatalf("invalid request accepted: %s", raw)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid scope reached native backend")
	}
	r, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"inspect_window","id":"proc-owned"}`))
	if err != nil || r.Err != nil || calls.Load() != 1 {
		t.Fatalf("owned action failed: %v %v", err, r.Err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, err = tool.Execute(ctx, json.RawMessage(`{"action":"inspect_window","id":"proc-owned"}`))
	if err != nil || r.Err == nil || calls.Load() != 1 {
		t.Fatal("cancelled action reached backend")
	}
	tool.Manager.items["proc-owned"].windowBirth++
	r, err = tool.Execute(context.Background(), json.RawMessage(`{"action":"inspect_window","id":"proc-owned"}`))
	if err != nil || r.Err == nil || calls.Load() != 1 {
		t.Fatal("reused process identity accepted")
	}
}
