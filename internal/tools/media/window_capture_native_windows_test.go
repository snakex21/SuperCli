//go:build windows

package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Fixtures own every captured pixel. Tests never capture the desktop or user windows.
type syntheticWindow struct {
	HWND    uintptr
	Title   string
	Printed chan struct{}
	Release chan struct{}
	Done    chan struct{}
}

func paintSyntheticWindow(dc uintptr, mode string) {
	left := nativeWindowRect{Right: 128, Bottom: 160}
	right := nativeWindowRect{Left: 128, Right: 256, Bottom: 160}
	redColor, blueColor := uintptr(0x000000FF), uintptr(0x00FF0000)
	if mode == "blank" {
		redColor, blueColor = 0, 0
	}
	red, _, _ := captureGDI32.NewProc("CreateSolidBrush").Call(redColor)
	blue, _, _ := captureGDI32.NewProc("CreateSolidBrush").Call(blueColor)
	captureUser32.NewProc("FillRect").Call(dc, uintptr(unsafe.Pointer(&left)), red)
	captureUser32.NewProc("FillRect").Call(dc, uintptr(unsafe.Pointer(&right)), blue)
	captureGDI32.NewProc("DeleteObject").Call(red)
	captureGDI32.NewProc("DeleteObject").Call(blue)
	captureGDI32.NewProc("GdiFlush").Call()
}

func newSyntheticWindow(t testing.TB, mode string) *syntheticWindow {
	t.Helper()
	fixture := &syntheticWindow{Title: fmt.Sprintf("SuperCli capture fixture %d %s", time.Now().UnixNano(), mode), Printed: make(chan struct{}, 1), Release: make(chan struct{}), Done: make(chan struct{})}
	ready := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(fixture.Done)
		instance, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW").Call(0)
		className, _ := windows.UTF16PtrFromString(fixture.Title)
		title, _ := windows.UTF16PtrFromString(fixture.Title)
		proc := windows.NewCallback(func(hwnd, message, wparam, lparam uintptr) uintptr {
			switch message {
			case 0x000F: // WM_PAINT, including PrintWindow's compositor repaint.
				paint := struct {
					DC                 uintptr
					Erase              int32
					Rect               nativeWindowRect
					Restore, IncUpdate int32
					Reserved           [32]byte
				}{}
				dc := wparam
				if dc == 0 {
					dc, _, _ = captureUser32.NewProc("BeginPaint").Call(hwnd, uintptr(unsafe.Pointer(&paint)))
				}
				paintSyntheticWindow(dc, mode)
				if wparam == 0 {
					captureUser32.NewProc("EndPaint").Call(hwnd, uintptr(unsafe.Pointer(&paint)))
				}
				return 0
			case 0x0317, 0x0318: // A classic owner may also handle WM_PRINT/WM_PRINTCLIENT.
				paintSyntheticWindow(wparam, mode)
				return 1
			case 0x0010:
				captureUser32.NewProc("DestroyWindow").Call(hwnd)
				return 0
			case 0x0002:
				captureUser32.NewProc("PostQuitMessage").Call(0)
				return 0
			}
			result, _, _ := captureUser32.NewProc("DefWindowProcW").Call(hwnd, message, wparam, lparam)
			return result
		})
		class := struct {
			Size, Style                        uint32
			Proc                               uintptr
			ClassExtra, WindowExtra            int32
			Instance, Icon, Cursor, Background uintptr
			MenuName, ClassName                *uint16
			SmallIcon                          uintptr
		}{Proc: proc, Instance: instance, ClassName: className}
		class.Size = uint32(unsafe.Sizeof(class))
		atom, _, err := captureUser32.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&class)))
		if atom == 0 {
			ready <- fmt.Errorf("register synthetic window: %v", err)
			return
		}
		defer captureUser32.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(className)), instance)
		hwnd, _, err := captureUser32.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)), 0x00CF0000, 20, 20, 256, 160, 0, 0, instance, 0)
		if hwnd == 0 {
			ready <- fmt.Errorf("create synthetic window: %v", err)
			return
		}
		fixture.HWND = hwnd
		// Place only our fixture at the bottom, without activating it.
		captureUser32.NewProc("SetWindowPos").Call(hwnd, 1, 0, 0, 0, 0, 0x0010|0x0001|0x0002|0x0040)
		captureUser32.NewProc("ShowWindow").Call(hwnd, 4)
		captureUser32.NewProc("UpdateWindow").Call(hwnd)
		captureGDI32.NewProc("GdiFlush").Call()
		windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmFlush").Call()
		ready <- nil
		var message struct {
			HWND           uintptr
			Message        uint32
			WParam, LParam uintptr
			Time           uint32
			X, Y           int32
			Private        uint32
		}
		for {
			result, _, _ := captureUser32.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
			if int32(result) <= 0 {
				return
			}
			captureUser32.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&message)))
			captureUser32.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&message)))
		}
	}()
	if err := <-ready; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		close(fixture.Release)
		captureUser32.NewProc("PostMessageW").Call(fixture.HWND, 0x0010, 0, 0)
		select {
		case <-fixture.Done:
		case <-time.After(5 * time.Second):
			t.Error("synthetic window did not close")
		}
	})
	return fixture
}

func assertSyntheticColors(t testing.TB, data []byte, mime string, info WindowInfo) {
	t.Helper()
	image, err := png.Decode(bytes.NewReader(data))
	if err != nil || mime != "image/png" || image.Bounds().Dx() != 256 || image.Bounds().Dy() != 160 {
		t.Fatalf("invalid synthetic image: mime=%s info=%+v err=%v", mime, info, err)
	}
	if got := color.NRGBAModel.Convert(image.At(64, 80)); got != (color.NRGBA{R: 255, A: 255}) {
		t.Fatalf("left synthetic pixel=%v", got)
	}
	if got := color.NRGBAModel.Convert(image.At(192, 80)); got != (color.NRGBA{B: 255, A: 255}) {
		t.Fatalf("right synthetic pixel=%v", got)
	}
}

func TestNativeBackgroundWindowCapture(t *testing.T) {
	fixture := newSyntheticWindow(t, "colors")
	foreground, _, _ := captureUser32.NewProc("GetForegroundWindow").Call()
	before, err := inspectNativeWindow(fixture.HWND)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	started := time.Now()
	data, mime, info, err := captureWindow(ctx, WindowSelector{Title: fixture.Title})
	if err != nil {
		t.Fatal(err)
	}
	assertSyntheticColors(t, data, mime, info)
	after, err := inspectNativeWindow(fixture.HWND)
	nowForeground, _, _ := captureUser32.NewProc("GetForegroundWindow").Call()
	if err != nil || info.PID != uint32(os.Getpid()) || info.Title != fixture.Title || after.Minimized != before.Minimized || after.Visible != before.Visible || nowForeground != foreground {
		t.Fatalf("capture changed fixture state: before=%+v after=%+v info=%+v foregroundChanged=%t err=%v", before, after, info, nowForeground != foreground, err)
	}
	t.Logf("covered synthetic capture: %dx%d %d bytes, %s; foreground unchanged", info.Width, info.Height, len(data), time.Since(started))
}

func TestNativeMinimizedWindowRefusesIncompleteContent(t *testing.T) {
	fixture := newSyntheticWindow(t, "colors")
	foreground, _, _ := captureUser32.NewProc("GetForegroundWindow").Call()
	captureUser32.NewProc("ShowWindow").Call(fixture.HWND, 7) // SW_SHOWMINNOACTIVE.
	before, err := inspectNativeWindow(fixture.HWND)
	if err != nil {
		t.Fatal(err)
	}
	data, _, _, err := captureWindow(context.Background(), WindowSelector{HWND: fmt.Sprintf("0x%X", fixture.HWND)})
	if err == nil || !strings.Contains(err.Error(), "minimized") || !strings.Contains(err.Error(), "incomplete") || len(data) != 0 {
		t.Fatalf("minimized classic window=%d bytes err=%v", len(data), err)
	}
	after, stateErr := inspectNativeWindow(fixture.HWND)
	nowForeground, _, _ := captureUser32.NewProc("GetForegroundWindow").Call()
	if stateErr != nil || !before.Minimized || !after.Minimized || after.Visible != before.Visible || foreground != nowForeground {
		t.Fatalf("refusal changed window state: before=%+v after=%+v err=%v", before, after, stateErr)
	}
}

func TestNativeWindowBlankAndReplacementFailWithoutFallback(t *testing.T) {
	fixture := newSyntheticWindow(t, "blank")
	data, _, _, err := captureWindow(context.Background(), WindowSelector{HWND: fmt.Sprintf("0x%X", fixture.HWND)})
	if err == nil || !strings.Contains(err.Error(), "blank or incomplete") || len(data) != 0 {
		t.Fatalf("blank capture=%d bytes err=%v", len(data), err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	data, err = runWindowHelper(ctx, windowHelperRequest{Mode: "capture", HWND: fmt.Sprintf("0x%X", fixture.HWND), ExpectedPID: uint32(os.Getpid()) + 1}, DefaultMaxScreenshotBytes)
	if err == nil || len(data) != 0 {
		t.Fatalf("replaced capture=%d bytes err=%v", len(data), err)
	}
}

func TestNativeProcessWindowCaptureKeepsForegroundAndExactOwner(t *testing.T) {
	fixture := newSyntheticWindow(t, "colors")
	foreground, _, _ := captureUser32.NewProc("GetForegroundWindow").Call()
	for _, selector := range []WindowSelector{
		{PID: uint32(os.Getpid())},
		{PID: uint32(os.Getpid()), Title: fixture.Title},
		{PID: uint32(os.Getpid()), HWND: fmt.Sprintf("0x%X", fixture.HWND)},
	} {
		data, mime, info, err := captureWindow(context.Background(), selector)
		if err != nil {
			t.Fatal(err)
		}
		assertSyntheticColors(t, data, mime, info)
		if info.PID != selector.PID || info.HWND != fmt.Sprintf("0x%X", fixture.HWND) {
			t.Fatalf("wrong process window: %+v", info)
		}
	}
	if after, _, _ := captureUser32.NewProc("GetForegroundWindow").Call(); foreground != after {
		t.Fatal("process capture changed foreground")
	}
	if data, _, _, err := captureWindow(context.Background(), WindowSelector{PID: uint32(os.Getpid()), Title: "missing-owned-window"}); err == nil || len(data) != 0 {
		t.Fatalf("missing target capture=%d err=%v", len(data), err)
	}
}

func TestNativeWindowBlockedHelperProcess(t *testing.T) {
	if os.Getenv("SUPERCLI_TEST_BLOCK_WINDOW_HELPER") != "1" {
		return
	}
	time.Sleep(30 * time.Second)
	os.Exit(0)
}

func TestNativeWindowHungHelperIsCanceledAndReaped(t *testing.T) {
	// PrintWindow has no timeout API. The exact parent wait/kill path is checked
	// with a blocked helper; compositor caching makes a stuck paint callback an
	// unreliable way to force PrintWindow itself to block.
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeWindowBlockedHelperProcess$")
	cmd.Env = append(os.Environ(), "SUPERCLI_TEST_BLOCK_WINDOW_HELPER=1")
	started := time.Now()
	data, err := captureCommand(ctx, cmd, DefaultMaxScreenshotBytes)
	if !errors.Is(err, context.DeadlineExceeded) || len(data) != 0 {
		t.Fatalf("hung helper cancellation=%v bytes=%d", err, len(data))
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("canceled window helper was not promptly reaped")
	}
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("capture helper was not reaped")
	}
}

func TestNativeWindowCaptureCanceledBeforeLaunch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, err := captureWindow(ctx, WindowSelector{Title: "anything"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := listCaptureWindows(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestNativeWindowCaptureLaunchCost(t *testing.T) {
	fixture := newSyntheticWindow(t, "colors")
	var single, double []time.Duration
	for i := 0; i < 5; i++ {
		started := time.Now()
		data, mime, info, err := captureWindow(context.Background(), WindowSelector{Title: fixture.Title})
		if err != nil {
			t.Fatal(err)
		}
		single = append(single, time.Since(started))
		assertSyntheticColors(t, data, mime, info)
		started = time.Now()
		infos, err := listCaptureWindows(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		selected, err := selectCaptureWindow(infos, WindowSelector{Title: fixture.Title})
		if err != nil {
			t.Fatal(err)
		}
		data, mime, info, err = captureWindow(context.Background(), WindowSelector{HWND: selected.HWND})
		if err != nil {
			t.Fatal(err)
		}
		double = append(double, time.Since(started))
		assertSyntheticColors(t, data, mime, info)
	}
	sort.Slice(single, func(i, j int) bool { return single[i] < single[j] })
	sort.Slice(double, func(i, j int) bool { return double[i] < double[j] })
	t.Logf("five synthetic pairs, median: resolve+capture one helper=%s, separate discovery+capture two helpers=%s", single[2], double[2])
}
