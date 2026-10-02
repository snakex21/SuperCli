package processsession

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"supercli/internal/tools/core"
)

func screenshotFixture(t *testing.T) (*Tool, *process) {
	t.Helper()
	tool := New(t.TempDir())
	item := &process{id: "proc-fixture", status: "running", pid: 4321}
	tool.Manager.items[item.id] = item
	return tool, item
}

func TestOwnedScreenshotPreservesNativeResultAndSelection(t *testing.T) {
	tool, item := screenshotFixture(t)
	image := &core.ImageContent{MediaType: "image/png", Data: []byte{1, 2, 3}}
	otherImage := &core.ImageContent{MediaType: "image/jpeg", Data: []byte{4}}
	want := core.Result{Text: `{"source":"window","path":"portable/snapshots/window.png","media":{"type":"image"}}`, Image: image, Images: []*core.ImageContent{otherImage}}
	calls := 0
	tool.captureScreenshot = func(ctx context.Context, raw json.RawMessage) (core.Result, error) {
		calls++
		var a map[string]any
		if err := json.Unmarshal(raw, &a); err != nil {
			t.Fatal(err)
		}
		if a["source"] != "window" || a["process_id"] != float64(item.pid) || a["window_title"] != "Owned editor" || a["attach"] != false {
			t.Fatalf("wrong owned-window arguments: %+v", a)
		}
		if _, ok := a["window_id"]; ok {
			t.Fatal("unowned window selector forwarded")
		}
		return want, nil
	}
	got, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"screenshot","id":"proc-fixture","window_title":" Owned editor ","process_id":9999,"window_id":"unowned"}`))
	if err != nil || got.Err != nil || calls != 1 || got.Text != want.Text || got.Image != image || len(got.Images) != 1 || got.Images[0] != otherImage {
		t.Fatalf("native result changed: %+v err=%v calls=%d", got, err, calls)
	}
}

func TestOwnedScreenshotAttachmentIsExplicit(t *testing.T) {
	tool, _ := screenshotFixture(t)
	tool.captureScreenshot = func(_ context.Context, raw json.RawMessage) (core.Result, error) {
		var a struct {
			Attach bool   `json:"attach"`
			Detail string `json:"image_detail"`
		}
		if err := json.Unmarshal(raw, &a); err != nil || !a.Attach || a.Detail != "original" {
			t.Fatalf("explicit analysis arguments lost: %s err=%v", raw, err)
		}
		return core.Result{Image: &core.ImageContent{MediaType: "image/png", Data: []byte{1}}}, nil
	}
	got, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"screenshot","id":"proc-fixture","attach":true,"image_detail":"original"}`))
	if err != nil || got.Err != nil || got.Image == nil {
		t.Fatalf("analysis image lost: %+v %v", got, err)
	}
}

func TestOwnedScreenshotRejectsInvalidOrInactiveTargetBeforeCapture(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  string
		setup func(*Tool, *process)
	}{
		{"missing id", `{"action":"screenshot"}`, nil},
		{"unknown id", `{"action":"screenshot","id":"proc-other"}`, nil},
		{"finished", `{"action":"screenshot","id":"proc-fixture"}`, func(_ *Tool, p *process) { p.status = "done" }},
		{"exited while draining", `{"action":"screenshot","id":"proc-fixture"}`, func(_ *Tool, p *process) { p.exited.Store(true) }},
		{"stop requested", `{"action":"screenshot","id":"proc-fixture"}`, func(_ *Tool, p *process) { p.stopRequested = true }},
		{"missing native PID", `{"action":"screenshot","id":"proc-fixture"}`, func(_ *Tool, p *process) { p.pid = 0 }},
		{"closed manager", `{"action":"screenshot","id":"proc-fixture"}`, func(tool *Tool, _ *process) { tool.Manager.closed = true }},
		{"bad image detail", `{"action":"screenshot","id":"proc-fixture","image_detail":"bad"}`, nil},
		{"oversized title", `{"action":"screenshot","id":"proc-fixture","window_title":"` + strings.Repeat("a", 513) + `"}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool, p := screenshotFixture(t)
			if tc.setup != nil {
				tc.setup(tool, p)
			}
			tool.captureScreenshot = func(context.Context, json.RawMessage) (core.Result, error) {
				t.Fatal("inactive/invalid target reached capture")
				return core.Result{}, nil
			}
			got, err := tool.Execute(context.Background(), json.RawMessage(tc.args))
			if err != nil || got.Err == nil || got.Text != "" || got.Image != nil {
				t.Fatalf("target was not rejected: %+v %v", got, err)
			}
		})
	}
}

func TestOwnedScreenshotRejectsExitStopOrCancelDuringCapture(t *testing.T) {
	for _, mode := range []string{"exit", "stop", "close", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			tool, p := screenshotFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tool.captureScreenshot = func(context.Context, json.RawMessage) (core.Result, error) {
				switch mode {
				case "exit":
					p.exited.Store(true)
				case "stop":
					p.mu.Lock()
					p.stopRequested = true
					p.mu.Unlock()
				case "close":
					tool.Manager.mu.Lock()
					tool.Manager.closed = true
					tool.Manager.mu.Unlock()
				case "cancel":
					cancel()
				}
				return core.Result{Text: "do not show unowned pixels", Image: &core.ImageContent{Data: []byte{1}}}, nil
			}
			got, err := tool.Execute(ctx, json.RawMessage(`{"action":"screenshot","id":"proc-fixture"}`))
			if err != nil || got.Err == nil || got.Image != nil || got.Text != "" {
				t.Fatalf("stale capture escaped lifecycle guard: %+v %v", got, err)
			}
			if mode == "cancel" && !errors.Is(got.Err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", got.Err)
			}
		})
	}
}

func TestOwnedScreenshotPreservesNativeFailureWithoutFallback(t *testing.T) {
	tool, _ := screenshotFixture(t)
	want := errors.New("owned process has no renderable window")
	calls := 0
	tool.captureScreenshot = func(_ context.Context, raw json.RawMessage) (core.Result, error) {
		calls++
		if !strings.Contains(string(raw), `"source":"window"`) {
			t.Fatal("desktop fallback")
		}
		return core.Result{Err: want}, want
	}
	got, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"screenshot","id":"proc-fixture"}`))
	if !errors.Is(err, want) || !errors.Is(got.Err, want) || calls != 1 {
		t.Fatalf("failure changed/retried: %+v %v calls=%d", got, err, calls)
	}
}

func TestOwnedScreenshotStoresDirectNativePIDAndRejectsCompletedProcess(t *testing.T) {
	tool := New(t.TempDir())
	defer tool.Close()
	start, err := execute(t, tool, map[string]any{"action": "start", "command": helperCommand("stdin"), "env": []string{"SUPERCLI_PROCESS_HELPER=1"}, "yield_ms": 0})
	if err != nil {
		t.Fatal(err)
	}
	item, err := tool.Manager.get(start.ID)
	if err != nil || item.pid <= 0 || item.pid == os.Getpid() {
		t.Fatalf("missing native owned PID: %+v %v", item, err)
	}
	captured := 0
	tool.captureScreenshot = func(_ context.Context, raw json.RawMessage) (core.Result, error) {
		captured++
		var args struct {
			PID int `json:"process_id"`
		}
		if err := json.Unmarshal(raw, &args); err != nil || args.PID != item.pid {
			t.Fatalf("native PID forwarding: %s %v", raw, err)
		}
		return core.Result{Text: `{"media":{"type":"image","path":"portable/fixture.png"}}`}, nil
	}
	raw, _ := json.Marshal(map[string]any{"action": "screenshot", "id": start.ID})
	got, err := tool.Execute(context.Background(), raw)
	if err != nil || got.Err != nil || captured != 1 {
		t.Fatalf("owned capture failed: %+v %v", got, err)
	}
	if _, err := tool.Manager.Write(start.ID, "finish", true); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := tool.Manager.Wait(ctx, start.ID); err != nil {
		t.Fatal(err)
	}
	got, err = tool.Execute(context.Background(), raw)
	if err != nil || got.Err == nil || captured != 1 {
		t.Fatalf("completed PID was reused: %+v %v capture=%d", got, err, captured)
	}
}

func TestOwnedScreenshotConstructorKeepsPortableDataDir(t *testing.T) {
	if got := New("workspace").DataDir; got != "workspace" {
		t.Fatalf("legacy constructor changed: %q", got)
	}
	if got := New("workspace", "portable-data").DataDir; got != "portable-data" {
		t.Fatalf("portable data lost: %q", got)
	}
	if got := New("workspace", "").DataDir; got != "workspace" {
		t.Fatalf("empty portable data fallback: %q", got)
	}
}

func TestOwnedScreenshotExitGuardPrecedesOutputDrain(t *testing.T) {
	stdinReader, stdinWriter := io.Pipe()
	defer stdinReader.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	checked := make(chan error, 1)
	p := &process{id: "proc-draining", pid: 4321, status: "running", done: make(chan struct{}), stdin: stdinWriter, cancel: cancel, started: time.Now(), stdout: newStreamBuffer(128), stderr: newStreamBuffer(128)}
	p.streams.Add(1)
	p.waitFn = func() (int, error) { return 0, nil }
	p.killFn = func() error { t.Error("exited fixture was killed"); return nil }
	p.closeOutput = func() {
		_, err := p.screenshotPID()
		checked <- err
		p.streams.Done()
	}
	go p.wait(ctx)
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		t.Fatal("output-drain fixture did not complete")
	}
	if err := <-checked; err == nil {
		t.Fatal("OS exit still authorized capture during output drain")
	}
}
