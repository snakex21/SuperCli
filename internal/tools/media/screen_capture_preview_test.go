package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type screenshotMetadata struct {
	Type, Source, Path string
	PreviewPath        string `json:"preview_path"`
	Bytes              int
	Attached           bool
}

func TestScreenshotScreenAndDisplayOnly(t *testing.T) {
	dir := t.TempDir()
	tool := NewSendScreenshot(dir, nil)
	tool.Capture = fakeCapture{err: errors.New("clipboard should not be used")}
	tool.ScreenCapture = func(ctx context.Context) ([]byte, string, error) { return pngHeader, "image/png", ctx.Err() }
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"source":"screen","attach":false}`))
	if err != nil || res.Err != nil || res.Image != nil {
		t.Fatalf("result=%+v err=%v", res, err)
	}
	var metadata screenshotMetadata
	if err := json.Unmarshal([]byte(res.Text), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Type != "image" || metadata.Source != "screen" || metadata.Attached || metadata.Bytes != len(pngHeader) {
		t.Fatalf("metadata=%+v", metadata)
	}
	expectedDir := filepath.Join(dir, ".supercli", "snapshots")
	if !filepath.IsAbs(metadata.Path) {
		t.Fatalf("path=%s", metadata.Path)
	}
	assertSameMediaFile(t, filepath.Dir(metadata.Path), expectedDir)
	if metadata.PreviewPath != "snapshot:"+filepath.Base(metadata.Path) {
		t.Fatalf("portable preview=%q", metadata.PreviewPath)
	}
	data, err := os.ReadFile(metadata.Path)
	if err != nil || !bytes.Equal(data, pngHeader) {
		t.Fatalf("saved bytes=%x err=%v", data, err)
	}
}

func TestScreenshotNoImplicitDataFallback(t *testing.T) {
	tool := NewSendScreenshot("", nil)
	tool.Capture = fakeCapture{data: pngHeader, mediaType: "image/png"}
	res, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err == nil || res.Err == nil || res.Image != nil {
		t.Fatalf("fallback=%+v err=%v", res, err)
	}
}

func TestScreenshotContextAndSaveFailure(t *testing.T) {
	tool := NewSendScreenshot(t.TempDir(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	tool.ScreenCapture = func(ctx context.Context) ([]byte, string, error) {
		calls++
		cancel()
		return pngHeader, "image/png", nil
	}
	res, err := tool.Execute(ctx, json.RawMessage(`{"source":"screen"}`))
	if !errors.Is(err, context.Canceled) || !errors.Is(res.Err, context.Canceled) || calls != 1 {
		t.Fatalf("result=%+v err=%v calls=%d", res, err, calls)
	}
	entries, _ := os.ReadDir(tool.BaseDir)
	if len(entries) != 0 {
		t.Fatal("cancelled capture wrote a snapshot")
	}
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	tool = NewSendScreenshot(blocker, nil)
	tool.Capture = fakeCapture{data: pngHeader, mediaType: "image/png"}
	res, err = tool.Execute(context.Background(), json.RawMessage(`{"attach":false}`))
	if err == nil || res.Err == nil || res.Image != nil || res.Text != "" {
		t.Fatalf("false saved success=%+v err=%v", res, err)
	}
	res, err = tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil || res.Err != nil || res.Image == nil {
		t.Fatalf("legacy attachment fallback=%+v err=%v", res, err)
	}
	var metadata map[string]any
	if json.Unmarshal([]byte(res.Text), &metadata) != nil || metadata["path"] != nil || metadata["save_error"] == nil {
		t.Fatalf("false preview path=%s", res.Text)
	}
}

func TestScreenshotConcurrentFilesAndLegacyArguments(t *testing.T) {
	tool := NewSendScreenshot(t.TempDir(), nil)
	tool.Capture = fakeCapture{data: pngHeader, mediaType: "image/png"}
	const count = 24
	paths := make(chan string, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := tool.Execute(context.Background(), json.RawMessage(`{"attach":false}`))
			if err != nil {
				errs <- err
				return
			}
			var m screenshotMetadata
			if err := json.Unmarshal([]byte(res.Text), &m); err != nil {
				errs <- err
				return
			}
			paths <- m.Path
		}()
	}
	wg.Wait()
	close(paths)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for p := range paths {
		if seen[p] {
			t.Fatalf("collision %s", p)
		}
		seen[p] = true
	}
	if len(seen) != count {
		t.Fatalf("files=%d", len(seen))
	}
	reg := NewRegistry()
	reg.MustRegister(tool.Spec())
	for _, args := range []string{`{"model":"legacy","attach":false}`, `{"model":"legacy","source":"clipboard"}`} {
		res, err := reg.Execute(context.Background(), "send_screenshot", json.RawMessage(args))
		if err != nil || res.Err != nil {
			t.Fatalf("args=%s result=%+v err=%v", args, res, err)
		}
	}
	for _, args := range []string{`{"model":"legacy","source":"oops"}`, `{"model":"legacy","unknown":1}`, `{"attach":[]}`} {
		res, err := reg.Execute(context.Background(), "send_screenshot", json.RawMessage(args))
		if err == nil && res.Err == nil {
			t.Fatalf("invalid args accepted: %s", args)
		}
	}
}

func TestCaptureBufferBoundAndContext(t *testing.T) {
	var b bytes.Buffer
	w := &captureBuffer{buffer: &b, remaining: 4}
	if n, err := w.Write([]byte("abc")); n != 3 || err != nil {
		t.Fatalf("write=%d %v", n, err)
	}
	if n, err := w.Write([]byte("de")); n != 0 || err == nil || b.String() != "abc" {
		t.Fatalf("overflow=%d %v %q", n, err, b.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.ctx = ctx
	if _, err := w.Write([]byte("x")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestScreenshotRejectsFalseMimeAndEmptyPayload(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("not PNG"), {0xff, 0xd8, 0xff}, []byte("\x89PNGwrong")} {
		dir := t.TempDir()
		tool := NewSendScreenshot(dir, nil)
		tool.Capture = fakeCapture{data: data, mediaType: "image/png"}
		res, err := tool.Execute(context.Background(), json.RawMessage(`{"attach":false}`))
		if err == nil || res.Err == nil || res.Text != "" {
			t.Fatalf("false success=%+v %v", res, err)
		}
		files, _ := os.ReadDir(dir)
		if len(files) != 0 {
			t.Fatal("invalid image saved")
		}
	}
}

func TestDarwinClipboardDescriptor(t *testing.T) {
	decoded, err := decodeDarwinClipboardPNG("  «data PNGf89504E470D0A1A0A»\n")
	if err != nil || !bytes.Equal(decoded, pngHeader) {
		t.Fatalf("decoded=%x err=%v", decoded, err)
	}
	for _, s := range []string{"", "ERROR: no image", "89504e470d0a1a0a", "«data PNGf9805e474d0a0a1a0»", "«data PNGfxyz»", "«data PNGf»", "«data JPEGffd8ff»"} {
		if _, err := decodeDarwinClipboardPNG(s); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}

func TestLinuxClipboardDistinguishesInstalledHelperFailure(t *testing.T) {
	find := func(name string) (string, error) {
		if name == "xclip" {
			return name, nil
		}
		return "", os.ErrNotExist
	}
	calls := 0
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		calls++
		return nil, errors.New("cannot open display")
	}
	_, _, err := captureClipboardLinuxWith(context.Background(), find, run)
	if err == nil || !strings.Contains(err.Error(), "cannot open display") || strings.Contains(err.Error(), "install") || calls != 1 {
		t.Fatalf("misleading error=%v calls=%d", err, calls)
	}
	find = func(string) (string, error) { return "", os.ErrNotExist }
	_, _, err = captureClipboardLinuxWith(context.Background(), find, run)
	if err == nil || !strings.Contains(err.Error(), "install xclip or wl-paste") || calls != 1 {
		t.Fatalf("missing helper=%v calls=%d", err, calls)
	}
	find = func(name string) (string, error) { return name, nil }
	run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		calls++
		if name == "xclip" {
			return nil, errors.New("X11 unavailable")
		}
		return pngHeader, nil
	}
	out, mime, err := captureClipboardLinuxWith(context.Background(), find, run)
	if err != nil || mime != "image/png" || !bytes.Equal(out, pngHeader) || calls != 3 {
		t.Fatalf("fallback=%x %s %v calls=%d", out, mime, err, calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	run = func(context.Context, string, ...string) ([]byte, error) {
		cancel()
		return nil, errors.New("interrupted")
	}
	_, _, err = captureClipboardLinuxWith(ctx, find, run)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestCaptureHelperProcess(t *testing.T) {
	if os.Getenv("SUPERCLI_CAPTURE_HELPER") != "1" {
		return
	}
	if os.Getenv("SUPERCLI_CAPTURE_WAIT") == "1" {
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	_, _ = os.Stdout.Write([]byte("too much capture output"))
	os.Exit(0)
}

func TestCaptureCommandLimitsAndCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCaptureHelperProcess$")
	cmd.Env = append(os.Environ(), "SUPERCLI_CAPTURE_HELPER=1")
	if out, err := captureCommand(ctx, cmd, 3); err == nil || len(out) != 0 {
		t.Fatalf("unbounded result=%q err=%v", out, err)
	}
	short, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stop()
	cmd = exec.CommandContext(short, os.Args[0], "-test.run=^TestCaptureHelperProcess$")
	cmd.Env = append(os.Environ(), "SUPERCLI_CAPTURE_HELPER=1", "SUPERCLI_CAPTURE_WAIT=1")
	if _, err := captureCommand(short, cmd, 32); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancel=%v", err)
	}
}
