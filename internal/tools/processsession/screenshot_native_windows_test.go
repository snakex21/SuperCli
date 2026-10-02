//go:build windows

package processsession

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var ownedWindowUser32 = windows.NewLazySystemDLL("user32.dll")
var ownedWindowGDI32 = windows.NewLazySystemDLL("gdi32.dll")
var ownedWindowKernel32 = windows.NewLazySystemDLL("kernel32.dll")

type ownedWindowRect struct{ Left, Top, Right, Bottom int32 }

// This helper creates only a synthetic child-owned window. Every captured pixel
// belongs to this fixture; no desktop or user window is ever captured.
func TestOwnedWindowFixtureHelper(t *testing.T) {
	if os.Getenv("SUPERCLI_OWNED_WINDOW_HELPER") != "1" {
		return
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	paint := func(dc uintptr) {
		left, right := ownedWindowRect{Right: 128, Bottom: 160}, ownedWindowRect{Left: 128, Right: 256, Bottom: 160}
		red, _, _ := ownedWindowGDI32.NewProc("CreateSolidBrush").Call(0x000000FF)
		blue, _, _ := ownedWindowGDI32.NewProc("CreateSolidBrush").Call(0x00FF0000)
		ownedWindowUser32.NewProc("FillRect").Call(dc, uintptr(unsafe.Pointer(&left)), red)
		ownedWindowUser32.NewProc("FillRect").Call(dc, uintptr(unsafe.Pointer(&right)), blue)
		ownedWindowGDI32.NewProc("DeleteObject").Call(red)
		ownedWindowGDI32.NewProc("DeleteObject").Call(blue)
		ownedWindowGDI32.NewProc("GdiFlush").Call()
	}
	instance, _, _ := ownedWindowKernel32.NewProc("GetModuleHandleW").Call(0)
	titleText := os.Getenv("SUPERCLI_OWNED_WINDOW_TITLE")
	className, err := windows.UTF16PtrFromString(titleText)
	if err != nil {
		t.Fatal(err)
	}
	callback := windows.NewCallback(func(hwnd, message, wparam, lparam uintptr) uintptr {
		switch message {
		case 0x000F:
			state := struct {
				DC                 uintptr
				Erase              int32
				Rect               ownedWindowRect
				Restore, IncUpdate int32
				Reserved           [32]byte
			}{}
			dc := wparam
			if dc == 0 {
				dc, _, _ = ownedWindowUser32.NewProc("BeginPaint").Call(hwnd, uintptr(unsafe.Pointer(&state)))
			}
			paint(dc)
			if wparam == 0 {
				ownedWindowUser32.NewProc("EndPaint").Call(hwnd, uintptr(unsafe.Pointer(&state)))
			}
			return 0
		case 0x0317, 0x0318:
			paint(wparam)
			return 1
		case 0x0010:
			ownedWindowUser32.NewProc("DestroyWindow").Call(hwnd)
			return 0
		case 0x0002:
			ownedWindowUser32.NewProc("PostQuitMessage").Call(0)
			return 0
		}
		result, _, _ := ownedWindowUser32.NewProc("DefWindowProcW").Call(hwnd, message, wparam, lparam)
		return result
	})
	class := struct {
		Size, Style                        uint32
		Proc                               uintptr
		ClassExtra, WindowExtra            int32
		Instance, Icon, Cursor, Background uintptr
		MenuName, ClassName                *uint16
		SmallIcon                          uintptr
	}{Proc: callback, Instance: instance, ClassName: className}
	class.Size = uint32(unsafe.Sizeof(class))
	atom, _, err := ownedWindowUser32.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&class)))
	if atom == 0 {
		t.Fatalf("register owned fixture: %v", err)
	}
	defer ownedWindowUser32.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(className)), instance)
	hwnd, _, err := ownedWindowUser32.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(className)), 0x00CF0000, 20, 20, 256, 160, 0, 0, instance, 0)
	if hwnd == 0 {
		t.Fatalf("create owned fixture: %v", err)
	}
	// Show at the bottom without activation; never focus or restore user windows.
	ownedWindowUser32.NewProc("SetWindowPos").Call(hwnd, 1, 0, 0, 0, 0, 0x0010|0x0001|0x0002|0x0040)
	ownedWindowUser32.NewProc("ShowWindow").Call(hwnd, 4)
	ownedWindowUser32.NewProc("UpdateWindow").Call(hwnd)
	ownedWindowGDI32.NewProc("GdiFlush").Call()
	windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmFlush").Call()
	if os.Getenv("SUPERCLI_OWNED_WINDOW_MINIMIZED") == "1" {
		ownedWindowUser32.NewProc("ShowWindow").Call(hwnd, 7)
	}
	// Stdout records readiness. A one-shot kernel event provides a blocking
	// completion notification, so the parent never polls output/progress.
	fmt.Printf("fixture_ready hwnd=0x%X\n", hwnd)
	eventName, _ := windows.UTF16PtrFromString(os.Getenv("SUPERCLI_OWNED_WINDOW_READY"))
	event, _, err := ownedWindowKernel32.NewProc("OpenEventW").Call(0x0002, 0, uintptr(unsafe.Pointer(eventName)))
	if event == 0 {
		t.Fatalf("open ready event: %v", err)
	}
	ownedWindowKernel32.NewProc("SetEvent").Call(event)
	windows.CloseHandle(windows.Handle(event))
	var message struct {
		HWND           uintptr
		Message        uint32
		WParam, LParam uintptr
		Time           uint32
		X, Y           int32
		Private        uint32
	}
	for {
		result, _, _ := ownedWindowUser32.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(result) <= 0 {
			return
		}
		ownedWindowUser32.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&message)))
		ownedWindowUser32.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&message)))
	}
}

func TestNativeOwnedSessionScreenshotKeepsForegroundAndPortablePreview(t *testing.T) {
	for _, minimized := range []bool{false, true} {
		name := "covered"
		if minimized {
			name = "minimized"
		}
		t.Run(name, func(t *testing.T) {
			// Keep all integration artifacts in the application workspace .tmp.
			tmpRoot, err := filepath.Abs(filepath.Join("..", "..", "..", "..", ".tmp", "owned-window-tests"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(tmpRoot, 0700); err != nil {
				t.Fatal(err)
			}
			caseDir, err := os.MkdirTemp(tmpRoot, "capture-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				rel, err := filepath.Rel(tmpRoot, caseDir)
				if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
					t.Errorf("unsafe fixture cleanup target")
					return
				}
				if err := os.RemoveAll(caseDir); err != nil {
					t.Error(err)
				}
			})
			workspace, dataDir := filepath.Join(caseDir, "workspace"), filepath.Join(caseDir, "portable-data")
			if err := os.MkdirAll(workspace, 0700); err != nil {
				t.Fatal(err)
			}
			tool := New(workspace, dataDir)
			defer tool.Close()
			title := fmt.Sprintf("SuperCli owned child fixture %d", time.Now().UnixNano())
			eventText := "Local\\SuperCliOwnedWindowReady" + fmt.Sprint(time.Now().UnixNano())
			eventName, _ := windows.UTF16PtrFromString(eventText)
			event, _, err := ownedWindowKernel32.NewProc("CreateEventW").Call(0, 1, 0, uintptr(unsafe.Pointer(eventName)))
			if event == 0 {
				t.Fatalf("create ready event: %v", err)
			}
			defer windows.CloseHandle(windows.Handle(event))
			env := []string{"SUPERCLI_OWNED_WINDOW_HELPER=1", "SUPERCLI_OWNED_WINDOW_TITLE=" + title, "SUPERCLI_OWNED_WINDOW_READY=" + eventText}
			if minimized {
				env = append(env, "SUPERCLI_OWNED_WINDOW_MINIMIZED=1")
			}
			// Public tool entry: the fixture executable is launched directly, not
			// through a shell whose child PID would need to be guessed.
			start, err := execute(t, tool, map[string]any{"action": "start", "command": []string{os.Args[0], "-test.run=TestOwnedWindowFixtureHelper"}, "env": env, "yield_ms": 0})
			if err != nil {
				t.Fatal(err)
			}
			wait, _, waitErr := ownedWindowKernel32.NewProc("WaitForSingleObject").Call(event, 5000)
			if wait != 0 {
				t.Fatalf("owned fixture readiness wait=%d err=%v", wait, waitErr)
			}
			foreground, _, _ := ownedWindowUser32.NewProc("GetForegroundWindow").Call()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			raw, _ := json.Marshal(map[string]any{"action": "screenshot", "id": start.ID})
			shot, err := tool.Execute(ctx, raw)
			if after, _, _ := ownedWindowUser32.NewProc("GetForegroundWindow").Call(); after != foreground {
				t.Fatal("owned capture changed foreground")
			}
			if minimized {
				if err == nil && shot.Err == nil {
					t.Fatal("minimized classic window unexpectedly accepted")
				}
				failure := fmt.Sprint(err, shot.Err)
				if !strings.Contains(failure, "minimized") || shot.Image != nil || shot.Text != "" {
					t.Fatalf("minimized limit concealed: %s", failure)
				}
				return
			}
			if err != nil || shot.Err != nil {
				t.Fatalf("native owned capture failed: %v / %v", err, shot.Err)
			}
			var metadata struct {
				Type, Source, Path string
				PreviewPath        string `json:"preview_path"`
				Attached           bool
				Window             struct {
					PID   uint32
					Title string
				}
			}
			if err := json.Unmarshal([]byte(shot.Text), &metadata); err != nil {
				t.Fatal(err)
			}
			item, err := tool.Manager.get(start.ID)
			if err != nil || metadata.Type != "image" || metadata.Source != "window" || metadata.Attached || shot.Image != nil || metadata.Window.PID != uint32(item.pid) || metadata.Window.Title != title {
				t.Fatalf("native media/ownership metadata mismatch: %+v err=%v", metadata, err)
			}
			rel, err := filepath.Rel(dataDir, metadata.Path)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) || !strings.HasPrefix(metadata.PreviewPath, "snapshot:") {
				t.Fatalf("preview escaped portable application data: %q err=%v", metadata.Path, err)
			}
			data, err := os.ReadFile(metadata.Path)
			if err != nil {
				t.Fatal(err)
			}
			image, err := png.Decode(bytes.NewReader(data))
			if err != nil || image.Bounds().Dx() != 256 || image.Bounds().Dy() != 160 {
				t.Fatalf("invalid owned PNG: %v", err)
			}
			if got := color.NRGBAModel.Convert(image.At(64, 80)); got != (color.NRGBA{R: 255, A: 255}) {
				t.Fatalf("owned left pixel=%v", got)
			}
			if got := color.NRGBAModel.Convert(image.At(192, 80)); got != (color.NRGBA{B: 255, A: 255}) {
				t.Fatalf("owned right pixel=%v", got)
			}
			ready, _, _ := item.stdout.readFrom(0, 2048)
			if !strings.Contains(string(ready), "fixture_ready hwnd=") {
				t.Fatal("fixture did not record stdout readiness")
			}
			t.Logf("direct owned window -> portable PNG/chat preview; foreground unchanged; %d bytes", len(data))
		})
	}
}
