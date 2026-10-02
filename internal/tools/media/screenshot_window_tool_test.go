package media

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestScreenshotTargetWindowAndPixelDefaults(t *testing.T) {
	for _, tc := range []struct {
		args, source string
		attached     bool
	}{
		{`{"source":"screen"}`, "screen", false},
		{`{"source":"screen","attach":true}`, "screen", true},
		{`{"source":"window","window_title":"Fixture"}`, "window", false},
		{`{"window_title":"Fixture"}`, "window", false},
		{`{"source":"screen","window_id":"0x123"}`, "window", false},
		{`{"window_id":"0x123","attach":true}`, "window", true},
		{`{}`, "clipboard", true},
		{`{"source":"clipboard","attach":false}`, "clipboard", false},
	} {
		t.Run(tc.args, func(t *testing.T) {
			tool := NewSendScreenshot(t.TempDir(), nil)
			tool.Capture = fakeCapture{data: pngHeader, mediaType: "image/png"}
			screenCalls, windowCalls := 0, 0
			tool.ScreenCapture = func(context.Context) ([]byte, string, error) {
				screenCalls++
				return pngHeader, "image/png", nil
			}
			tool.WindowCapture = func(ctx context.Context, selector WindowSelector) ([]byte, string, WindowInfo, error) {
				windowCalls++
				if selector.Title != "Fixture" && selector.HWND != "0x123" {
					t.Fatalf("wrong target: %+v", selector)
				}
				return pngHeader, "image/png", WindowInfo{HWND: "0x123", Title: "Fixture", PID: 123}, ctx.Err()
			}
			reg := NewRegistry()
			reg.MustRegister(tool.Spec())
			res, err := reg.Execute(context.Background(), "send_screenshot", json.RawMessage(tc.args))
			if err != nil || res.Err != nil {
				t.Fatalf("result=%+v error=%v", res, err)
			}
			var meta struct {
				Source, Path, PreviewPath string
				Attached                  bool
				Window                    *WindowInfo
			}
			if err := json.Unmarshal([]byte(res.Text), &meta); err != nil {
				t.Fatal(err)
			}
			if meta.Source != tc.source || meta.Attached != tc.attached || (res.Image != nil) != tc.attached {
				t.Fatalf("metadata=%+v pixels=%v", meta, res.Image != nil)
			}
			if tc.source == "window" && (windowCalls != 1 || screenCalls != 0 || meta.Window == nil || meta.Window.HWND != "0x123") {
				t.Fatalf("target capture=%d desktop=%d metadata=%+v", windowCalls, screenCalls, meta)
			}
			if tc.source == "screen" && (screenCalls != 1 || windowCalls != 0) {
				t.Fatalf("desktop=%d target=%d", screenCalls, windowCalls)
			}
			if _, err := os.Stat(meta.Path); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(res.Text, `"preview_path":"snapshot:`) {
				t.Fatalf("missing portable preview: %s", res.Text)
			}
		})
	}
}

func TestScreenshotWindowSelectionDoesNotFallBackToDesktop(t *testing.T) {
	for _, args := range []string{`{"source":"window"}`, `{"source":"clipboard","window_title":"Fixture"}`, `{"source":"windows","window_id":"0x123"}`} {
		tool := NewSendScreenshot(t.TempDir(), nil)
		calls := 0
		tool.ScreenCapture = func(context.Context) ([]byte, string, error) { calls++; return pngHeader, "image/png", nil }
		res, err := tool.Execute(context.Background(), json.RawMessage(args))
		if err == nil || res.Err == nil || calls != 0 {
			t.Fatalf("args=%s result=%+v err=%v desktop=%d", args, res, err, calls)
		}
	}
	tool := NewSendScreenshot(t.TempDir(), nil)
	expected := errors.New("ambiguous window title")
	tool.WindowCapture = func(context.Context, WindowSelector) ([]byte, string, WindowInfo, error) {
		return nil, "", WindowInfo{}, expected
	}
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"window_title":"Fixture"}`))
	if !errors.Is(err, expected) || !errors.Is(res.Err, expected) || res.Image != nil || res.Text != "" {
		t.Fatalf("result=%+v error=%v", res, err)
	}
	entries, _ := os.ReadDir(tool.BaseDir)
	if len(entries) != 0 {
		t.Fatal("failed capture persisted an image")
	}
}

func TestScreenshotWindowListIsBoundedAndDoesNotCapture(t *testing.T) {
	tool := NewSendScreenshot(t.TempDir(), nil)
	tool.WindowsList = func(context.Context) ([]WindowInfo, error) {
		windows := make([]WindowInfo, 45)
		for i := range windows {
			windows[i] = WindowInfo{HWND: "0x123", Title: "Fixture"}
		}
		return windows, nil
	}
	tool.ScreenCapture = func(context.Context) ([]byte, string, error) {
		t.Fatal("listing captured desktop")
		return nil, "", nil
	}
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"source":"windows"}`))
	var meta struct {
		Windows   []WindowInfo
		Truncated bool
	}
	if err != nil || res.Image != nil || json.Unmarshal([]byte(res.Text), &meta) != nil || len(meta.Windows) != 40 || !meta.Truncated {
		t.Fatalf("result=%+v error=%v metadata=%+v", res, err, meta)
	}
	entries, _ := os.ReadDir(tool.BaseDir)
	if len(entries) != 0 {
		t.Fatal("listing persisted data")
	}
	tool.WindowsList = func(context.Context) ([]WindowInfo, error) { return nil, nil }
	res, err = tool.Execute(context.Background(), json.RawMessage(`{"source":"windows"}`))
	if err != nil || res.Text != `{"windows":[]}` {
		t.Fatalf("empty list=%+v %v", res, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tool.WindowsList = func(context.Context) ([]WindowInfo, error) { t.Fatal("cancelled listing called OS"); return nil, nil }
	res, err = tool.Execute(ctx, json.RawMessage(`{"source":"windows"}`))
	if !errors.Is(err, context.Canceled) || !errors.Is(res.Err, context.Canceled) {
		t.Fatalf("result=%+v error=%v", res, err)
	}
}
