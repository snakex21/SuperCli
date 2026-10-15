//go:build windows

package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestNativeMinimizedSuccessfulRenderDoesNotRestore(t *testing.T) {
	fixture := newSyntheticWindow(t, "colors")
	captureUser32.NewProc("ShowWindow").Call(fixture.HWND, 7)
	calls := 0
	render := func(_ context.Context, hwnd uintptr, info WindowInfo, _ io.Writer, _ bool) error {
		calls++
		minimized, _, _ := captureUser32.NewProc("IsIconic").Call(hwnd)
		if !info.Minimized || minimized == 0 {
			t.Fatal("window was restored before trying minimized rendering")
		}
		return nil
	}
	if err := captureNativeWindowRendering(context.Background(), fixture.HWND, uint32(os.Getpid()), io.Discard, false, render); err != nil || calls != 1 {
		t.Fatalf("successful render was retried or needed recovery: calls=%d err=%v", calls, err)
	}
}

// The helper restores only our synthetic window, then is deliberately killed
// before its cleanup can execute. This covers the actual native state left by
// a canceled PrintWindow helper rather than merely testing a deferred function.
func TestNativeCaptureRecoveryBlockedHelper(t *testing.T) {
	text := os.Getenv("SUPERCLI_TEST_CAPTURE_RECOVERY")
	if text == "" {
		return
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	handle, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	mapping, err := mapWindowCaptureRecovery(windows.Handle(handle))
	if err != nil {
		t.Fatal(err)
	}
	defer mapping.close()
	hwnd, err := parseWindowHandle(os.Getenv("SUPERCLI_TEST_CAPTURE_HWND"))
	if err != nil {
		t.Fatal(err)
	}
	cleanup, err := peekMinimizedWindow(hwnd, mapping.record)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	name, _ := windows.UTF16PtrFromString(os.Getenv("SUPERCLI_TEST_CAPTURE_READY"))
	event, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(event)
	windows.SetEvent(event)
	time.Sleep(30 * time.Second) // canceled after the kernel event, never polled
	t.Fatal("parent did not terminate the blocked capture helper")
}

func TestNativeCanceledCaptureRestoresMinimizedWindow(t *testing.T) {
	fixture := newSyntheticWindow(t, "colors")
	captureUser32.NewProc("ShowWindow").Call(fixture.HWND, 7)
	get := "GetWindowLongPtrW"
	if unsafe.Sizeof(uintptr(0)) == 4 {
		get = "GetWindowLongW"
	}
	style, _, _ := captureUser32.NewProc(get).Call(fixture.HWND, ^uintptr(19))
	mapping, err := newWindowCaptureMapping()
	if err != nil {
		t.Fatal(err)
	}
	defer mapping.close()
	name := fmt.Sprintf("Local\\SuperCliCaptureRecovery%d", time.Now().UnixNano())
	ptr, _ := windows.UTF16PtrFromString(name)
	ready, err := windows.CreateEvent(nil, 1, 0, ptr)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(ready)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeCaptureRecoveryBlockedHelper$")
	cmd.Env = append(os.Environ(), fmt.Sprintf("SUPERCLI_TEST_CAPTURE_RECOVERY=%d", mapping.handle), fmt.Sprintf("SUPERCLI_TEST_CAPTURE_HWND=0x%X", fixture.HWND), "SUPERCLI_TEST_CAPTURE_READY="+name)
	mapping.inherit(cmd)
	observed := make(chan uint32, 1)
	go func() {
		state, _ := windows.WaitForSingleObject(ready, 15000)
		observed <- state
		cancel()
	}()
	data, err := runRecoverableWindowCapture(ctx, cmd, DefaultMaxScreenshotBytes, mapping)
	if state := <-observed; state != windows.WAIT_OBJECT_0 {
		t.Fatalf("helper failed before restoring the window: state=%d err=%v", state, err)
	}
	if !errors.Is(err, context.Canceled) || len(data) != 0 || cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("helper was not canceled/reaped: bytes=%d err=%v state=%v", len(data), err, cmd.ProcessState)
	}
	captureErr := err
	after, err := inspectNativeWindow(fixture.HWND)
	newStyle, _, _ := captureUser32.NewProc(get).Call(fixture.HWND, ^uintptr(19))
	if err != nil || !after.Minimized || style != newStyle || mapping.record.Active != 0 {
		t.Fatalf("parent cleanup failed: state=%+v styles=%#x/%#x active=%d err=%v capture=%v", after, style, newStyle, mapping.record.Active, err, captureErr)
	}
}

func TestNativeRecoveryDoesNotTouchReplacementIdentity(t *testing.T) {
	fixture := newSyntheticWindow(t, "colors")
	captureUser32.NewProc("ShowWindow").Call(fixture.HWND, 7)
	mapping, err := newWindowCaptureMapping()
	if err != nil {
		t.Fatal(err)
	}
	defer mapping.close()
	if err := mapping.record.prepare(fixture.HWND, 0); err != nil {
		t.Fatal(err)
	}
	created := mapping.record.Created
	mapping.record.Created++ // same PID/HWND, different process creation identity
	if err := mapping.record.restore(); err != nil || mapping.record.Active == 0 {
		t.Fatalf("a replaced process was touched: active=%d err=%v", mapping.record.Active, err)
	}
	property := captureRestoreProperty()
	captureUser32.NewProc("RemovePropW").Call(fixture.HWND, uintptr(unsafe.Pointer(property)))
	// Simulate HWND reuse in the same process: a new window has no transaction
	// property, even when process creation identity is unchanged.
	mapping.record.Created = created
	if err := mapping.record.restore(); err != nil || mapping.record.Active == 0 {
		t.Fatalf("a replacement HWND was touched: active=%d err=%v", mapping.record.Active, err)
	}
}
