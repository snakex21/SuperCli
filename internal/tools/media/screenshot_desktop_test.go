package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScreenshotDesktopRoutePreviewAndAttachment(t *testing.T) {
	for _, attached := range []bool{false, true} {
		t.Run(map[bool]string{false: "display-only", true: "pixel-analysis"}[attached], func(t *testing.T) {
			tool := NewSendScreenshot(t.TempDir(), nil)
			tool.Capture = fakeCapture{err: errors.New("desktop must not use clipboard")}
			tool.ScreenCapture = func(context.Context) ([]byte, string, error) {
				t.Fatal("desktop used visible screen")
				return nil, "", nil
			}
			tool.WindowCapture = func(context.Context, WindowSelector) ([]byte, string, WindowInfo, error) {
				t.Fatal("desktop inferred an application selector")
				return nil, "", WindowInfo{}, nil
			}
			calls := 0
			tool.DesktopCapture = func(ctx context.Context) ([]byte, string, WindowInfo, error) {
				calls++
				return pngHeader, "image/png", WindowInfo{HWND: "0x123", Title: "Synthetic shell", PID: 123}, ctx.Err()
			}
			reg := NewRegistry()
			reg.MustRegister(tool.Spec())
			args := json.RawMessage(`{"source":"desktop"}`)
			if attached {
				args = json.RawMessage(`{"source":"desktop","attach":true,"image_detail":"original"}`)
			}
			res, err := reg.Execute(context.Background(), "send_screenshot", args)
			if err != nil || res.Err != nil || calls != 1 || (res.Image != nil) != attached {
				t.Fatalf("result=%+v error=%v calls=%d", res, err, calls)
			}
			var meta struct {
				Type, Source, Path string
				PreviewPath        string `json:"preview_path"`
				MediaType          string `json:"media_type"`
				Attached           bool
				Window             *WindowInfo
			}
			if err := json.Unmarshal([]byte(res.Text), &meta); err != nil {
				t.Fatal(err)
			}
			if meta.Type != "image" || meta.Source != "desktop" || meta.Attached != attached || meta.MediaType != "image/png" || meta.Window == nil || meta.Window.PID != 123 {
				t.Fatalf("metadata=%+v", meta)
			}
			if !strings.HasPrefix(res.Text, `{"type":"image",`) || meta.PreviewPath != "snapshot:"+filepath.Base(meta.Path) {
				t.Fatalf("noncanonical preview=%s", res.Text)
			}
			assertSameMediaFile(t, filepath.Dir(meta.Path), filepath.Join(tool.BaseDir, ".supercli", "snapshots"))
			data, err := os.ReadFile(meta.Path)
			if err != nil || !bytes.Equal(data, pngHeader) {
				t.Fatalf("saved=%x error=%v", data, err)
			}
		})
	}
}

func TestScreenshotDesktopRejectsSelectorsAndNeverFallsBack(t *testing.T) {
	for _, args := range []string{
		`{"source":"desktop","window_title":"Fixture"}`,
		`{"source":"desktop","window_id":"0x123"}`,
		`{"source":"desktop","process_id":123}`,
	} {
		t.Run(args, func(t *testing.T) {
			tool := NewSendScreenshot(t.TempDir(), nil)
			tool.DesktopCapture = func(context.Context) ([]byte, string, WindowInfo, error) {
				t.Fatal("invalid desktop selector reached OS")
				return nil, "", WindowInfo{}, nil
			}
			tool.ScreenCapture = func(context.Context) ([]byte, string, error) {
				t.Fatal("invalid desktop selector used screen")
				return nil, "", nil
			}
			res, err := tool.Execute(context.Background(), json.RawMessage(args))
			if err == nil || res.Err == nil || res.Image != nil || res.Text != "" {
				t.Fatalf("result=%+v error=%v", res, err)
			}
			files, _ := os.ReadDir(tool.BaseDir)
			if len(files) != 0 {
				t.Fatal("invalid capture persisted data")
			}
		})
	}
	tool := NewSendScreenshot(t.TempDir(), nil)
	expected := errors.New("unsupported shell layout")
	calls := 0
	tool.DesktopCapture = func(context.Context) ([]byte, string, WindowInfo, error) {
		calls++
		return nil, "", WindowInfo{}, expected
	}
	tool.ScreenCapture = func(context.Context) ([]byte, string, error) {
		t.Fatal("failed desktop capture used screen")
		return nil, "", nil
	}
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"source":"desktop"}`))
	if !errors.Is(err, expected) || !errors.Is(res.Err, expected) || calls != 1 || res.Image != nil || res.Text != "" {
		t.Fatalf("result=%+v error=%v calls=%d", res, err, calls)
	}
	files, _ := os.ReadDir(tool.BaseDir)
	if len(files) != 0 {
		t.Fatal("failed capture persisted data")
	}
}

func TestScreenshotDesktopCancellationDoesNotSave(t *testing.T) {
	for _, cancelBefore := range []bool{false, true} {
		tool := NewSendScreenshot(t.TempDir(), nil)
		ctx, cancel := context.WithCancel(context.Background())
		if cancelBefore {
			cancel()
		}
		calls := 0
		tool.DesktopCapture = func(context.Context) ([]byte, string, WindowInfo, error) {
			calls++
			cancel()
			return pngHeader, "image/png", WindowInfo{}, nil
		}
		res, err := tool.Execute(ctx, json.RawMessage(`{"source":"desktop"}`))
		cancel()
		if !errors.Is(err, context.Canceled) || !errors.Is(res.Err, context.Canceled) || res.Text != "" || res.Image != nil || (cancelBefore && calls != 0) || (!cancelBefore && calls != 1) {
			t.Fatalf("before=%t result=%+v error=%v calls=%d", cancelBefore, res, err, calls)
		}
		files, _ := os.ReadDir(tool.BaseDir)
		if len(files) != 0 {
			t.Fatal("cancelled capture persisted data")
		}
	}
}
