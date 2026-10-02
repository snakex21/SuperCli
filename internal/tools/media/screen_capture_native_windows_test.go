//go:build windows

package media

import (
	"bytes"
	"context"
	"image/png"
	"os"
	"testing"
	"time"
)

// Opt-in native smoke captures only into memory and records dimensions, never
// writes or publishes desktop pixels. Normal tests never capture the desktop.
func TestNativeScreenCapture(t *testing.T) {
	if os.Getenv("SUPERCLI_TEST_NATIVE_SCREEN") != "1" {
		t.Skip("native screen smoke not requested")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	data, mime, err := captureScreen(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if mime != "image/png" || len(data) > DefaultMaxScreenshotBytes {
		t.Fatalf("mime=%s bytes=%d", mime, len(data))
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 {
		t.Fatalf("config=%+v err=%v", cfg, err)
	}
	t.Logf("native screen capture: %dx%d, %d encoded bytes", cfg.Width, cfg.Height, len(data))
}
