//go:build windows

package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"
)

// Resolver injection limits native test capture to our existing painted fixture.
// The real shell is tested only in explicitly authorized, ignored smoke fixtures.
func TestNativeDesktopResolvedFixtureKeepsForeground(t *testing.T) {
	fixture := newSyntheticWindow(t, "colors")
	foreground, _, _ := captureUser32.NewProc("GetForegroundWindow").Call()
	before, err := inspectNativeWindow(fixture.HWND)
	if err != nil {
		t.Fatal(err)
	}
	tool := NewSendScreenshot(t.TempDir(), nil)
	tool.DesktopCapture = func(ctx context.Context) ([]byte, string, WindowInfo, error) {
		var output bytes.Buffer
		if err := captureNativeDesktopWith(ctx, func() uintptr { return fixture.HWND }, &output); err != nil {
			return nil, "", WindowInfo{}, err
		}
		return decodeNativeWindowCapture(ctx, output.Bytes(), before.PID)
	}
	started := time.Now()
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"source":"desktop"}`))
	if err != nil || res.Err != nil || res.Image != nil {
		t.Fatalf("result=%+v error=%v", res, err)
	}
	var meta struct {
		Source, Path string
		PreviewPath  string `json:"preview_path"`
		Window       *WindowInfo
	}
	if err := json.Unmarshal([]byte(res.Text), &meta); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(meta.Path)
	if err != nil || meta.Source != "desktop" || meta.Window == nil || !strings.HasPrefix(meta.PreviewPath, "snapshot:") {
		t.Fatalf("metadata=%+v error=%v", meta, err)
	}
	assertSyntheticColors(t, data, "image/png", *meta.Window)
	after, err := inspectNativeWindow(fixture.HWND)
	nowForeground, _, _ := captureUser32.NewProc("GetForegroundWindow").Call()
	if err != nil || foreground != nowForeground || before.Minimized != after.Minimized || before.Visible != after.Visible {
		t.Fatalf("changed fixture state: before=%+v after=%+v foregroundChanged=%t error=%v", before, after, foreground != nowForeground, err)
	}
	t.Logf("owned desktop resolver fixture: %dx%d %d bytes, %s; foreground unchanged", meta.Window.Width, meta.Window.Height, len(data), time.Since(started))
}

func TestNativeDesktopUnavailableChangedAndCancelled(t *testing.T) {
	var output bytes.Buffer
	if err := captureNativeDesktopWith(context.Background(), func() uintptr { return 0 }, &output); err == nil || !strings.Contains(err.Error(), "unavailable") || output.Len() != 0 {
		t.Fatalf("missing shell output=%d error=%v", output.Len(), err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := captureNativeDesktopWith(ctx, func() uintptr { t.Fatal("cancelled capture resolved shell"); return 0 }, &output); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	fixture := newSyntheticWindow(t, "colors")
	calls := 0
	resolve := func() uintptr {
		calls++
		if calls == 1 {
			return fixture.HWND
		}
		return 0
	}
	if err := captureNativeDesktopWith(context.Background(), resolve, &output); err == nil || !strings.Contains(err.Error(), "changed during capture") {
		t.Fatalf("stale desktop accepted: calls=%d error=%v", calls, err)
	}
}

func TestNativeDesktopHelperRejectsApplicationSelectors(t *testing.T) {
	for _, raw := range []string{
		`{"mode":"desktop","hwnd":"0x123"}`,
		`{"mode":"desktop","title":"Fixture"}`,
		`{"mode":"desktop","expected_pid":123}`,
	} {
		var output bytes.Buffer
		err := runWindowCaptureHelper(strings.NewReader(raw), &output)
		if err == nil || !strings.Contains(err.Error(), "does not accept") || output.Len() != 0 {
			t.Fatalf("request=%s output=%d error=%v", raw, output.Len(), err)
		}
	}
}

func TestDesktopPixelPolicyPreservesStrictWindows(t *testing.T) {
	for _, solid := range [][3]byte{{0, 0, 0}, {0, 255, 0}} {
		pixels := make([]byte, 256*160*4)
		for i := 0; i < len(pixels); i += 4 {
			pixels[i], pixels[i+1], pixels[i+2] = solid[0], solid[1], solid[2]
		}
		if blankDesktopPixels(pixels) || !blankWindowPixels(pixels, 256, 160) {
			t.Fatal("painted solid desktop must be accepted while ordinary windows stay strict")
		}
	}
	if !blankDesktopPixels(nil) {
		t.Fatal("empty desktop accepted")
	}
	untouched := make([]byte, 256*160*4)
	for i := 0; i < len(untouched); i += 4 {
		untouched[i], untouched[i+1], untouched[i+2] = 0x23, 0x45, 0x67
	}
	if !blankDesktopPixels(untouched) || !blankWindowPixels(untouched, 256, 160) {
		t.Fatal("untouched sentinel frame accepted")
	}
	for i := 0; i < len(untouched)/4; i += 4 {
		untouched[i], untouched[i+1], untouched[i+2] = 0, 0, 0
	}
	if !blankDesktopPixels(untouched) {
		t.Fatal("majority untouched desktop accepted")
	}
}

func TestNativeDesktopPaintedBlackAndSolid(t *testing.T) {
	for _, mode := range []string{"blank", "solid"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newSyntheticWindow(t, mode)
			foreground, _, _ := captureUser32.NewProc("GetForegroundWindow").Call()
			var output bytes.Buffer
			if err := captureNativeDesktopWith(context.Background(), func() uintptr { return fixture.HWND }, &output); err != nil {
				t.Fatal(err)
			}
			data, mime, info, err := decodeNativeWindowCapture(context.Background(), output.Bytes(), 0)
			if err != nil || mime != "image/png" || info.Width != 256 || info.Height != 160 {
				t.Fatalf("solid desktop=%+v mime=%s error=%v", info, mime, err)
			}
			image, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			expected := color.NRGBA{A: 255}
			if mode == "solid" {
				expected.G = 255
			}
			if got := color.NRGBAModel.Convert(image.At(128, 80)); got != expected {
				t.Fatalf("solid desktop pixel=%v want=%v", got, expected)
			}
			nowForeground, _, _ := captureUser32.NewProc("GetForegroundWindow").Call()
			if nowForeground != foreground {
				t.Fatal("desktop capture changed foreground")
			}
		})
	}
}
