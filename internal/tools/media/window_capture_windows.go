//go:build windows

package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	windowHelperEnv    = "SUPERCLI_INTERNAL_WINDOW_CAPTURE"
	windowHelperMarker = "native-window-v1"
	windowHelperArg    = "--supercli-internal-window-capture"
	maxWindowListBytes = 2 << 20
)

var (
	captureUser32 = windows.NewLazySystemDLL("user32.dll")
	captureGDI32  = windows.NewLazySystemDLL("gdi32.dll")
)

type windowHelperRequest struct {
	Mode           string `json:"mode"`
	HWND           string `json:"hwnd,omitempty"`
	Title          string `json:"title,omitempty"`
	ExpectedPID    uint32 `json:"expected_pid,omitempty"`
	RecoveryHandle uint64 `json:"recovery_handle,omitempty"`
}

// PrintWindow has no cancellation API. Run it in a short-lived hidden copy of
// this executable so a deadline can kill it and release every native resource.
// Both markers are required; an inherited environment variable alone is inert.
func init() {
	if len(os.Args) != 2 || os.Args[1] != windowHelperArg || os.Getenv(windowHelperEnv) != windowHelperMarker {
		return
	}
	if err := runWindowCaptureHelper(os.Stdin, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func runWindowCaptureHelper(input io.Reader, output io.Writer) error {
	var request windowHelperRequest
	decoder := json.NewDecoder(io.LimitReader(input, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return fmt.Errorf("invalid native capture request: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("invalid trailing native capture request")
	}
	switch request.Mode {
	case "desktop":
		if request.HWND != "" || request.Title != "" || request.ExpectedPID != 0 {
			return fmt.Errorf("desktop capture does not accept an application window selector")
		}
		return captureNativeDesktop(context.Background(), output)
	case "list":
		infos, err := listNativeCaptureWindows(context.Background())
		if err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(infos)
	case "inspect":
		hwnd, err := parseWindowHandle(request.HWND)
		if err != nil {
			return err
		}
		info, err := inspectNativeWindow(hwnd)
		if err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(info)
	case "capture":
		var recovery *windowCaptureMapping
		if request.RecoveryHandle != 0 {
			var err error
			recovery, err = mapWindowCaptureRecovery(windows.Handle(request.RecoveryHandle))
			if err != nil {
				return fmt.Errorf("capture recovery mapping: %w", err)
			}
			defer recovery.close()
		}
		selector := WindowSelector{Title: request.Title, HWND: request.HWND, PID: request.ExpectedPID}
		if err := validateWindowSelector(selector); err != nil {
			return err
		}
		// Hold the requested process open through capture: Windows cannot reuse
		// its PID while this handle exists. Never discover an unrelated foreground
		// window if the launched process exits or has not supplied a GUI window.
		var process windows.Handle
		if selector.PID != 0 {
			var err error
			process, err = windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, selector.PID)
			if err != nil {
				return fmt.Errorf("capture process_id %d: %w", selector.PID, err)
			}
			defer windows.CloseHandle(process)
			if err := requireCaptureProcessAlive(process); err != nil {
				return err
			}
		}
		if request.Title != "" || request.HWND == "" {
			infos, err := listNativeProcessWindows(context.Background(), selector.PID)
			if err != nil {
				return err
			}
			if len(infos) == 0 && process != 0 {
				// Only a process without windows needs an initialization wait.
				// A busy, existing window must not pay another two seconds on
				// every screenshot. One event wait, then one fresh enumeration.
				idle, _, _ := captureUser32.NewProc("WaitForInputIdle").Call(uintptr(process), 2000)
				if idle == 0 {
					infos, err = listNativeProcessWindows(context.Background(), selector.PID)
					if err != nil {
						return err
					}
				}
			}
			selected, err := selectCaptureWindow(infos, selector)
			if err != nil {
				return err
			}
			request.HWND, request.ExpectedPID = selected.HWND, selected.PID
		}
		hwnd, err := parseWindowHandle(request.HWND)
		if err != nil {
			return err
		}
		captureCtx := context.Background()
		if recovery != nil {
			captureCtx = context.WithValue(captureCtx, windowCaptureRecoveryKey{}, recovery.record)
		}
		if err := captureNativeWindow(captureCtx, hwnd, request.ExpectedPID, output); err != nil {
			return err
		}
		if process != 0 {
			return requireCaptureProcessAlive(process)
		}
		return nil
	default:
		return fmt.Errorf("unknown native capture operation")
	}
}

func runWindowHelper(ctx context.Context, request windowHelperRequest, limit int) ([]byte, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	var recovery *windowCaptureMapping
	if request.Mode == "capture" {
		recovery, err = newWindowCaptureMapping()
		if err != nil {
			return nil, fmt.Errorf("capture recovery mapping: %w", err)
		}
		defer recovery.close()
		request.RecoveryHandle = uint64(recovery.handle)
	}
	input, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, executable, windowHelperArg)
	cmd.Stdin = bytes.NewReader(input)
	for _, value := range os.Environ() {
		if !strings.EqualFold(strings.SplitN(value, "=", 2)[0], windowHelperEnv) {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, windowHelperEnv+"="+windowHelperMarker)
	if recovery != nil {
		recovery.inherit(cmd)
		return runRecoverableWindowCapture(ctx, cmd, limit, recovery)
	}
	return captureCommand(ctx, cmd, limit)
}

func listCaptureWindows(ctx context.Context) ([]WindowInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, screenshotCaptureTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := runWindowHelper(ctx, windowHelperRequest{Mode: "list"}, maxWindowListBytes)
	if err != nil {
		return nil, err
	}
	var infos []WindowInfo
	if err := json.Unmarshal(data, &infos); err != nil {
		return nil, fmt.Errorf("invalid native window list: %w", err)
	}
	return infos, ctx.Err()
}

func captureWindow(ctx context.Context, selector WindowSelector) ([]byte, string, WindowInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, screenshotCaptureTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, "", WindowInfo{}, err
	}
	if err := validateWindowSelector(selector); err != nil {
		return nil, "", WindowInfo{}, err
	}
	request := windowHelperRequest{Mode: "capture", HWND: strings.TrimSpace(selector.HWND), Title: strings.TrimSpace(selector.Title), ExpectedPID: selector.PID}
	data, err := runWindowHelper(ctx, request, DefaultMaxScreenshotBytes+4096)
	if err != nil {
		return nil, "", WindowInfo{}, err
	}
	return decodeNativeWindowCapture(ctx, data, selector.PID)
}

func captureDesktop(ctx context.Context) ([]byte, string, WindowInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, screenshotCaptureTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, "", WindowInfo{}, err
	}
	data, err := runWindowHelper(ctx, windowHelperRequest{Mode: "desktop"}, DefaultMaxScreenshotBytes+4096)
	if err != nil {
		return nil, "", WindowInfo{}, err
	}
	return decodeNativeWindowCapture(ctx, data, 0)
}

func decodeNativeWindowCapture(ctx context.Context, data []byte, expectedPID uint32) ([]byte, string, WindowInfo, error) {
	split := bytes.IndexByte(data, '\n')
	if split < 0 || split > 4096 {
		return nil, "", WindowInfo{}, fmt.Errorf("invalid native window capture metadata")
	}
	var info WindowInfo
	if err := json.Unmarshal(data[:split], &info); err != nil {
		return nil, "", WindowInfo{}, fmt.Errorf("invalid native window capture metadata: %w", err)
	}
	if expectedPID != 0 && info.PID != expectedPID {
		return nil, "", WindowInfo{}, fmt.Errorf("native capture returned a different process")
	}
	pixels := data[split+1:]
	if len(pixels) > DefaultMaxScreenshotBytes || sniffMediaType(pixels) != "image/png" {
		return nil, "", WindowInfo{}, fmt.Errorf("native window capture did not produce bounded PNG")
	}
	if err := ctx.Err(); err != nil {
		return nil, "", WindowInfo{}, err
	}
	return pixels, "image/png", info, nil
}

func nativeShellWindow() uintptr {
	hwnd, _, _ := captureUser32.NewProc("GetShellWindow").Call()
	return hwnd
}

func captureNativeDesktop(ctx context.Context, output io.Writer) error {
	return captureNativeDesktopWith(ctx, nativeShellWindow, output)
}

// GetShellWindow identifies the shell desktop directly; no foreground lookup,
// guessed title, Explorer message, activation or screen fallback is used.
// The injectable resolver lets native tests own every captured pixel.
func captureNativeDesktopWith(ctx context.Context, shellWindow func() uintptr, output io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	hwnd := shellWindow()
	if hwnd == 0 {
		return fmt.Errorf("shell desktop is unavailable")
	}
	info, err := inspectNativeWindow(hwnd)
	if err != nil {
		return fmt.Errorf("shell desktop: %w", err)
	}
	process, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, info.PID)
	if err != nil {
		return fmt.Errorf("shell desktop owner: %w", err)
	}
	defer windows.CloseHandle(process)
	if err := requireCaptureProcessAlive(process); err != nil {
		return err
	}
	if err := validateNativeDesktopTopology(ctx, hwnd, info.PID); err != nil {
		return err
	}
	if err := captureNativeWindowWith(ctx, hwnd, info.PID, output, true); err != nil {
		return err
	}
	if shellWindow() != hwnd {
		return fmt.Errorf("shell desktop changed during capture")
	}
	return requireCaptureProcessAlive(process)
}

// Some Explorer layouts move the icon view onto a sibling WorkerW. Rendering
// only Progman would then return wallpaper without the requested icons. Fail
// honestly for that known split instead of photographing covering applications
// or trying to reparent/composite undocumented shell surfaces.
func validateNativeDesktopTopology(ctx context.Context, shell uintptr, pid uint32) error {
	var stopped error
	count := 0
	defView, _ := windows.UTF16PtrFromString("SHELLDLL_DefView")
	callback := windows.NewCallback(func(hwnd, _ uintptr) uintptr {
		if err := ctx.Err(); err != nil {
			stopped = err
			return 0
		}
		count++
		if count > 512 {
			stopped = fmt.Errorf("shell desktop discovery exceeds 512 windows")
			return 0
		}
		if hwnd == shell {
			return 1
		}
		var owner uint32
		captureUser32.NewProc("GetWindowThreadProcessId").Call(hwnd, uintptr(unsafe.Pointer(&owner)))
		if owner != pid {
			return 1
		}
		class := make([]uint16, 64)
		captureUser32.NewProc("GetClassNameW").Call(hwnd, uintptr(unsafe.Pointer(&class[0])), uintptr(len(class)))
		name := windows.UTF16ToString(class)
		if name != "WorkerW" && name != "Progman" {
			return 1
		}
		icons, _, _ := captureUser32.NewProc("FindWindowExW").Call(hwnd, 0, uintptr(unsafe.Pointer(defView)), 0)
		if icons != 0 {
			stopped = fmt.Errorf("shell desktop icons are on a separate %s surface; desktop-only background capture is unavailable for this shell layout", name)
			return 0
		}
		return 1
	})
	ok, _, err := captureUser32.NewProc("EnumWindows").Call(callback, 0)
	if stopped != nil {
		return stopped
	}
	if ok == 0 {
		return fmt.Errorf("enumerate shell desktop: %v", err)
	}
	return nil
}

type nativeWindowRect struct{ Left, Top, Right, Bottom int32 }
type nativeWindowPlacement struct {
	Length, Flags, ShowCmd uint32
	MinX, MinY, MaxX, MaxY int32
	Normal                 nativeWindowRect
}

func inspectNativeWindow(hwnd uintptr) (WindowInfo, error) {
	valid, _, _ := captureUser32.NewProc("IsWindow").Call(hwnd)
	ancestor, _, _ := captureUser32.NewProc("GetAncestor").Call(hwnd, 2) // GA_ROOT.
	if valid == 0 || ancestor != hwnd {
		return WindowInfo{}, fmt.Errorf("window_id %#x no longer identifies an open top-level window", hwnd)
	}
	var pid uint32
	thread, _, _ := captureUser32.NewProc("GetWindowThreadProcessId").Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if thread == 0 || pid == 0 {
		return WindowInfo{}, fmt.Errorf("window owner unavailable")
	}
	title := make([]uint16, 1025)
	captureUser32.NewProc("GetWindowTextW").Call(hwnd, uintptr(unsafe.Pointer(&title[0])), uintptr(len(title)))
	minimized, _, _ := captureUser32.NewProc("IsIconic").Call(hwnd)
	visible, _, _ := captureUser32.NewProc("IsWindowVisible").Call(hwnd)
	// The same PID can own invisible input-method helper windows. They are
	// available for explicit title/handle selection, never the default app target.
	styleProc := "GetWindowLongPtrW"
	if unsafe.Sizeof(uintptr(0)) == 4 {
		styleProc = "GetWindowLongW"
	}
	style, _, _ := captureUser32.NewProc(styleProc).Call(hwnd, ^uintptr(19)) // GWL_EXSTYLE (-20).
	class := make([]uint16, 256)
	captureUser32.NewProc("GetClassNameW").Call(hwnd, uintptr(unsafe.Pointer(&class[0])), uintptr(len(class)))
	className := windows.UTF16ToString(class)
	auxiliary := style&0x80 != 0 || className == "IME" || className == "MSCTFIME UI"
	return WindowInfo{HWND: fmt.Sprintf("0x%X", hwnd), Title: windows.UTF16ToString(title), PID: pid, Minimized: minimized != 0, Visible: visible != 0, auxiliary: auxiliary}, nil
}

func listNativeCaptureWindows(ctx context.Context) ([]WindowInfo, error) {
	return listNativeProcessWindows(ctx, 0)
}

func requireCaptureProcessAlive(process windows.Handle) error {
	status, err := windows.WaitForSingleObject(process, 0)
	if err != nil {
		return fmt.Errorf("capture process state unavailable: %w", err)
	}
	if status != uint32(windows.WAIT_TIMEOUT) {
		return fmt.Errorf("capture target process exited")
	}
	return nil
}

func listNativeProcessWindows(ctx context.Context, pid uint32) ([]WindowInfo, error) {
	var infos []WindowInfo
	var stopped error
	// This callback is created only inside the disposable helper process.
	callback := windows.NewCallback(func(hwnd, _ uintptr) uintptr {
		if err := ctx.Err(); err != nil {
			stopped = err
			return 0
		}
		if pid != 0 {
			var owner uint32
			captureUser32.NewProc("GetWindowThreadProcessId").Call(hwnd, uintptr(unsafe.Pointer(&owner)))
			if owner != pid {
				return 1 // do not query titles/styles of unrelated applications
			}
		}
		info, err := inspectNativeWindow(hwnd)
		if err == nil && (pid == 0 || info.PID == pid) && (info.Title != "" || info.Visible || info.Minimized) {
			if len(infos) >= 512 {
				stopped = fmt.Errorf("open window list exceeds 512 windows")
				return 0
			}
			infos = append(infos, info)
		}
		return 1
	})
	ok, _, err := captureUser32.NewProc("EnumWindows").Call(callback, 0)
	if stopped != nil {
		return nil, stopped
	}
	if ok == 0 {
		return nil, fmt.Errorf("enumerate open windows: %v", err)
	}
	if infos == nil {
		infos = []WindowInfo{}
	}
	return infos, nil
}

func captureNativeWindow(ctx context.Context, hwnd uintptr, expectedPID uint32, output io.Writer) error {
	return captureNativeWindowWith(ctx, hwnd, expectedPID, output, false)
}

func captureNativeWindowWith(ctx context.Context, hwnd uintptr, expectedPID uint32, output io.Writer, desktop bool) error {
	return captureNativeWindowRendering(ctx, hwnd, expectedPID, output, desktop, renderNativeWindow)
}

func captureNativeWindowRendering(ctx context.Context, hwnd uintptr, expectedPID uint32, output io.Writer, desktop bool, render func(context.Context, uintptr, WindowInfo, io.Writer, bool) error) error {
	restore := enterWindowCaptureDPI(hwnd)
	defer restore()
	info, err := inspectNativeWindow(hwnd)
	if err != nil {
		return err
	}
	if expectedPID != 0 && info.PID != expectedPID {
		return fmt.Errorf("selected window was closed or replaced; list windows again")
	}
	// Most classic windows render while minimized. Try that first: no style,
	// visibility, animation or Z-order changes and no fixed frame-settle delay.
	err = render(ctx, hwnd, info, output, desktop)
	if err == nil || !info.Minimized || desktop || !errors.Is(err, errWindowRender) {
		return err
	}
	recovery, _ := ctx.Value(windowCaptureRecoveryKey{}).(*windowCaptureRecovery)
	if recovery == nil {
		return fmt.Errorf("%w; temporary restore needs a parent recovery guard", err)
	}
	cleanup, peekErr := peekMinimizedWindow(hwnd, recovery)
	if peekErr != nil {
		return fmt.Errorf("%w; minimized restore: %v", err, peekErr)
	}
	defer cleanup() // also clean up on panic; parent covers a killed helper
	current, err := inspectNativeWindow(hwnd)
	if err == nil && current.PID != info.PID {
		err = fmt.Errorf("window ownership changed during capture")
	}
	if err == nil {
		err = render(ctx, hwnd, current, output, desktop)
	}
	return errors.Join(err, cleanup())
}

var errWindowRender = errors.New("application background rendering unavailable")

// Write metadata only when PNG encoding starts. A failed render has emitted
// nothing, so the minimized fallback cannot append a second partial frame.
type windowFrameWriter struct {
	io.Writer
	info    WindowInfo
	started bool
}

func (w *windowFrameWriter) Write(p []byte) (int, error) {
	if !w.started {
		w.started = true
		if err := json.NewEncoder(w.Writer).Encode(w.info); err != nil {
			return 0, err
		}
	}
	return w.Writer.Write(p)
}

func renderNativeWindow(ctx context.Context, hwnd uintptr, info WindowInfo, output io.Writer, desktop bool) error {
	var rect nativeWindowRect
	if info.Minimized {
		placement := nativeWindowPlacement{Length: uint32(unsafe.Sizeof(nativeWindowPlacement{}))}
		ok, _, err := captureUser32.NewProc("GetWindowPlacement").Call(hwnd, uintptr(unsafe.Pointer(&placement)))
		if ok == 0 {
			return fmt.Errorf("minimized window dimensions unavailable: %v", err)
		}
		rect = placement.Normal
	} else {
		ok, _, err := captureUser32.NewProc("GetWindowRect").Call(hwnd, uintptr(unsafe.Pointer(&rect)))
		if ok == 0 {
			return fmt.Errorf("window dimensions unavailable: %v", err)
		}
	}
	width, height := int64(rect.Right)-int64(rect.Left), int64(rect.Bottom)-int64(rect.Top)
	if err := validateCaptureDimensions(width, height); err != nil {
		return err
	}
	info.Width, info.Height = int(width), int(height)
	return writeWindowsBitmap(ctx, int32(width), int32(height), &windowFrameWriter{Writer: output, info: info}, func(_, mem uintptr, pixels []byte) error {
		var renderError error
		// Keep cross-process drawing inside PrintWindow. Sending WM_PRINT
		// directly with this helper's HDC does not reliably render another
		// process's window; the native fixture verifies the actual pixels.
		for _, flags := range []uintptr{2, 0} {
			if err := ctx.Err(); err != nil {
				return err
			}
			for i := 0; i < len(pixels); i += 4 {
				pixels[i], pixels[i+1], pixels[i+2] = 0x23, 0x45, 0x67
			}
			ok, _, err := captureUser32.NewProc("PrintWindow").Call(hwnd, mem, flags)
			if ok == 0 {
				renderError = fmt.Errorf("%w: window refused background capture: %v", errWindowRender, err)
				continue
			}
			flushed, _, err := captureGDI32.NewProc("GdiFlush").Call()
			if flushed == 0 {
				return fmt.Errorf("window capture flush: %v", err)
			}
			after, err := inspectNativeWindow(hwnd)
			if err != nil || after.PID != info.PID {
				return fmt.Errorf("selected window closed or changed during capture")
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if (desktop && !blankDesktopPixels(pixels)) || (!desktop && !blankWindowPixels(pixels, int(width), int(height))) {
				return nil
			}
			if desktop {
				renderError = fmt.Errorf("shell desktop returned untouched or incomplete content")
			} else if info.Minimized {
				renderError = fmt.Errorf("%w: minimized window returned blank or incomplete content", errWindowRender)
			} else {
				renderError = fmt.Errorf("%w: window returned blank or incomplete content", errWindowRender)
			}
		}
		return renderError
	})
}

// peekMinimizedWindow lets a minimized application render for one capture
// without the user seeing or touching it: the window becomes fully
// transparent and click-through, is restored without activation at the
// bottom of the Z-order, and the returned func minimizes it again (without
// activation) and restores its original extended style. Focus never moves.
// Classic GDI and DirectX programs paint nothing while minimized, so this is
// the only way to capture them while they stay on the taskbar.
func peekMinimizedWindow(hwnd uintptr, recovery *windowCaptureRecovery) (func() error, error) {
	// This mutex exists only for the capture, and also excludes other SuperCli
	// instances. No waiting/retry loop or resident window watcher is needed.
	name, _ := windows.UTF16PtrFromString(fmt.Sprintf("Local\\SuperCli.WindowCapture.%X", hwnd))
	mutex, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		return nil, err
	}
	status, err := windows.WaitForSingleObject(mutex, 0)
	if err != nil || (status != windows.WAIT_OBJECT_0 && status != windows.WAIT_ABANDONED) {
		windows.CloseHandle(mutex)
		return nil, fmt.Errorf("window already has an active capture")
	}
	unlock := func() { windows.ReleaseMutex(mutex); windows.CloseHandle(mutex) }
	get, set := "GetWindowLongPtrW", "SetWindowLongPtrW"
	if unsafe.Sizeof(uintptr(0)) == 4 {
		get, set = "GetWindowLongW", "SetWindowLongW"
	}
	const exStyle = ^uintptr(19) // GWL_EXSTYLE (-20).
	const layered, transparent = 0x00080000, 0x00000020
	original, _, _ := captureUser32.NewProc(get).Call(hwnd, exStyle)
	if original&layered != 0 {
		// The application owns its layering/alpha; changing it could not be
		// undone faithfully.
		unlock()
		return nil, fmt.Errorf("layered window")
	}
	if err := recovery.prepare(hwnd, original); err != nil {
		unlock()
		return nil, err
	}
	cleanup := func() error {
		err := recovery.restore()
		unlock()
		return err
	}
	// cleanup is idempotent, including its mutex handle.
	var once sync.Once
	var cleanupErr error
	finish := func() error { once.Do(func() { cleanupErr = cleanup() }); return cleanupErr }
	captureUser32.NewProc(set).Call(hwnd, exStyle, original|layered|transparent)
	ok, _, err := captureUser32.NewProc("SetLayeredWindowAttributes").Call(hwnd, 0, 0, 2) // LWA_ALPHA = 0.
	if ok == 0 {
		return nil, errors.Join(fmt.Errorf("transparent capture unavailable: %v", err), finish())
	}
	// Alpha is already zero before restore. Do not overwrite the application's
	// DWM transition preference: Windows exposes no corresponding getter.
	const noActivateBottom = 0x0010 | 0x0001 | 0x0002 // SWP_NOACTIVATE|NOSIZE|NOMOVE.
	captureUser32.NewProc("ShowWindow").Call(hwnd, 4) // SW_SHOWNOACTIVATE restores.
	captureUser32.NewProc("SetWindowPos").Call(hwnd, 1, 0, 0, 0, 0, noActivateBottom)
	captureUser32.NewProc("RedrawWindow").Call(hwnd, 0, 0, 0x0001|0x0080|0x0100|0x0004) // INVALIDATE|ALLCHILDREN|UPDATENOW|FRAME.
	windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmFlush").Call()
	// GPU programs resume rendering asynchronously after restore; give them
	// a few frames. This is a fixed bounded delay, not a readiness poll.
	time.Sleep(150 * time.Millisecond)
	return finish, nil
}

func enterCaptureDPI() func() { return enterCaptureDPIContext(^uintptr(3)) }

func enterWindowCaptureDPI(hwnd uintptr) func() {
	context := ^uintptr(3)
	get := captureUser32.NewProc("GetWindowDpiAwarenessContext")
	if get.Find() == nil {
		target, _, _ := get.Call(hwnd)
		if target != 0 {
			context = target
		}
	}
	return enterCaptureDPIContext(context)
}

func enterCaptureDPIContext(context uintptr) func() {
	runtime.LockOSThread()
	dpi := captureUser32.NewProc("SetThreadDpiAwarenessContext")
	var old uintptr
	if dpi.Find() == nil {
		old, _, _ = dpi.Call(context)
	}
	return func() {
		if old != 0 {
			dpi.Call(old)
		}
		runtime.UnlockOSThread()
	}
}

func validateCaptureDimensions(width, height int64) error {
	if width <= 0 || height <= 0 || width > 32768 || height > 32768 || width*height > 32<<20 {
		return fmt.Errorf("capture dimensions unavailable or exceed 32 megapixels")
	}
	return nil
}

func blankWindowPixels(pixels []byte, width, height int) bool {
	if len(pixels) < 4 {
		return true
	}
	var unpainted int
	uniform := true
	for i := 0; i < len(pixels); i += 4 {
		if pixels[i] == 0x23 && pixels[i+1] == 0x45 && pixels[i+2] == 0x67 {
			unpainted++
		}
		if pixels[i] != pixels[0] || pixels[i+1] != pixels[1] || pixels[i+2] != pixels[2] {
			uniform = false
		}
	}
	if uniform || unpainted > len(pixels)/8 {
		return true
	} // More than half was never painted.
	// A black client with a rendered caption is a common GPU/minimized failure.
	// Do not accept its frame as evidence that the application supplied content.
	if width > 64 && height > 80 {
		black := true
		for y := 40; y < height-16 && black; y++ {
			for x := 16; x < width-16; x++ {
				i := (y*width + x) * 4
				if pixels[i] != 0 || pixels[i+1] != 0 || pixels[i+2] != 0 {
					black = false
					break
				}
			}
		}
		if black {
			return true
		}
	}
	return false
}

// A solid-color wallpaper with icons hidden is a valid desktop. Keep the
// untouched-DIB test, but do not apply application-window uniform/black-client
// heuristics to the directly resolved shell desktop.
func blankDesktopPixels(pixels []byte) bool {
	if len(pixels) < 4 {
		return true
	}
	var unpainted int
	for i := 0; i < len(pixels); i += 4 {
		if pixels[i] == 0x23 && pixels[i+1] == 0x45 && pixels[i+2] == 0x67 {
			unpainted++
		}
	}
	return unpainted > len(pixels)/8 // Same majority-untouched limit as windows.
}

type windowOutputWriter struct {
	io.Writer
	Remaining int
	Context   context.Context
}

func (writer *windowOutputWriter) Write(p []byte) (int, error) {
	if err := writer.Context.Err(); err != nil {
		return 0, err
	}
	if len(p) > writer.Remaining {
		return 0, fmt.Errorf("capture output exceeds limit")
	}
	n, err := writer.Writer.Write(p)
	writer.Remaining -= n
	return n, err
}

// Both desktop and window capture share one bounded top-down DIB. Convert its
// BGRA bytes in place and encode directly; never allocate a second raw frame.
func writeWindowsBitmap(ctx context.Context, width, height int32, output io.Writer, render func(source, memory uintptr, pixels []byte) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateCaptureDimensions(int64(width), int64(height)); err != nil {
		return err
	}
	dc, _, err := captureUser32.NewProc("GetDC").Call(0)
	if dc == 0 {
		return fmt.Errorf("capture device context: %v", err)
	}
	defer captureUser32.NewProc("ReleaseDC").Call(0, dc)
	mem, _, err := captureGDI32.NewProc("CreateCompatibleDC").Call(dc)
	if mem == 0 {
		return fmt.Errorf("capture memory context: %v", err)
	}
	defer captureGDI32.NewProc("DeleteDC").Call(mem)
	info := struct {
		Size                   uint32
		Width, Height          int32
		Planes, BitCount       uint16
		Compression, SizeImage uint32
		XPels, YPels           int32
		Used, Important, Color uint32
	}{Size: 40, Width: width, Height: -height, Planes: 1, BitCount: 32}
	var bits unsafe.Pointer
	bitmap, _, err := captureGDI32.NewProc("CreateDIBSection").Call(dc, uintptr(unsafe.Pointer(&info)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bitmap == 0 || bits == nil {
		return fmt.Errorf("capture bitmap: %v", err)
	}
	defer captureGDI32.NewProc("DeleteObject").Call(bitmap)
	old, _, err := captureGDI32.NewProc("SelectObject").Call(mem, bitmap)
	if old == 0 || old == ^uintptr(0) {
		return fmt.Errorf("select capture bitmap: %v", err)
	}
	defer captureGDI32.NewProc("SelectObject").Call(mem, old)
	pixels := unsafe.Slice((*byte)(bits), int(width)*int(height)*4)
	if err := render(dc, mem, pixels); err != nil {
		return err
	}
	ok, _, err := captureGDI32.NewProc("GdiFlush").Call()
	if ok == 0 {
		return fmt.Errorf("capture flush: %v", err)
	}
	for row := 0; row < int(height); row++ {
		if row%64 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		for i := row * int(width) * 4; i < (row+1)*int(width)*4; i += 4 {
			pixels[i], pixels[i+2], pixels[i+3] = pixels[i+2], pixels[i], 255
		}
	}
	img := &image.NRGBA{Pix: pixels, Stride: int(width) * 4, Rect: image.Rect(0, 0, int(width), int(height))}
	writer := &windowOutputWriter{Writer: output, Remaining: DefaultMaxScreenshotBytes, Context: ctx}
	return (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(writer, img)
}
