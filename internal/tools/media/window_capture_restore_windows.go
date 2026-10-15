//go:build windows

package media

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"os/exec"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// A disposable PrintWindow helper can be killed at any instruction. Its defer
// alone cannot protect a temporarily restored window. Keep the small recovery
// record in inherited anonymous shared memory: the parent owns cleanup even on
// cancellation, helper failure, or output overflow. No file/cache is created.
type windowCaptureRecovery struct {
	Active        uint32
	PID           uint32
	HWND          uint64
	Created       uint64
	OriginalStyle uint64
	Token         uint64
}

type windowCaptureMapping struct {
	handle windows.Handle
	view   unsafe.Pointer
	record *windowCaptureRecovery
}

type windowCaptureRecoveryKey struct{}

func newWindowCaptureMapping() (*windowCaptureMapping, error) {
	security := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}
	handle, err := windows.CreateFileMapping(windows.InvalidHandle, &security, windows.PAGE_READWRITE, 0, uint32(unsafe.Sizeof(windowCaptureRecovery{})), nil)
	if err != nil {
		return nil, err
	}
	mapping, err := mapWindowCaptureRecovery(handle)
	if err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}
	var token [8]byte
	if _, err := rand.Read(token[:]); err != nil {
		mapping.close()
		return nil, err
	}
	mapping.record.Token = uint64(uintptr(binary.LittleEndian.Uint64(token[:]) | 1))
	return mapping, nil
}

func mapWindowCaptureRecovery(handle windows.Handle) (*windowCaptureMapping, error) {
	// NtMapViewOfSection exposes a typed PVOID out-parameter, so the native
	// address never passes through a stored uintptr and survives checkptr/vet.
	// This is the documented user-mode name of ZwMapViewOfSection (wdm.h).
	var view unsafe.Pointer
	size := unsafe.Sizeof(windowCaptureRecovery{})
	status, _, _ := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtMapViewOfSection").Call(
		uintptr(handle), uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&view)), 0, 0, 0,
		uintptr(unsafe.Pointer(&size)), 2, 0, windows.PAGE_READWRITE, // ViewUnmap: do not inherit the view
	)
	if int32(status) < 0 || view == nil {
		return nil, fmt.Errorf("map capture recovery: NTSTATUS 0x%08X", uint32(status))
	}
	return &windowCaptureMapping{handle: handle, view: view, record: (*windowCaptureRecovery)(view)}, nil
}

func (m *windowCaptureMapping) close() {
	windows.UnmapViewOfFile(uintptr(m.view))
	windows.CloseHandle(m.handle)
}

func (m *windowCaptureMapping) inherit(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.AdditionalInheritedHandles = append(cmd.SysProcAttr.AdditionalInheritedHandles, syscall.Handle(m.handle))
}

func runRecoverableWindowCapture(ctx context.Context, cmd *exec.Cmd, limit int, mapping *windowCaptureMapping) ([]byte, error) {
	data, captureErr := captureCommand(ctx, cmd, limit)
	if restoreErr := mapping.record.restore(); restoreErr != nil {
		return nil, errors.Join(captureErr, restoreErr)
	}
	return data, captureErr
}

func captureRestoreProperty() *uint16 {
	name, _ := windows.UTF16PtrFromString("SuperCli.TemporaryWindowCapture")
	return name
}

func captureProcessBirth(process windows.Handle) (uint64, error) {
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(process, &created, &exited, &kernel, &user); err != nil {
		return 0, err
	}
	return uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime), nil
}

func (r *windowCaptureRecovery) prepare(hwnd uintptr, original uintptr) error {
	var pid uint32
	captureUser32.NewProc("GetWindowThreadProcessId").Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	created, err := captureProcessBirth(process)
	if err != nil {
		return err
	}
	property := captureRestoreProperty()
	if token, _, _ := captureUser32.NewProc("GetPropW").Call(hwnd, uintptr(unsafe.Pointer(property))); token != 0 {
		return fmt.Errorf("window already has an active capture")
	}
	r.PID, r.HWND, r.Created, r.OriginalStyle = pid, uint64(hwnd), created, uint64(original)
	// Publish before any window mutation. A unique window property prevents
	// cleanup touching a replacement window even if its HWND is recycled.
	atomic.StoreUint32(&r.Active, 1)
	ok, _, err := captureUser32.NewProc("SetPropW").Call(hwnd, uintptr(unsafe.Pointer(property)), uintptr(r.Token))
	if ok == 0 {
		atomic.StoreUint32(&r.Active, 0)
		return fmt.Errorf("capture recovery unavailable: %v", err)
	}
	return nil
}

func (r *windowCaptureRecovery) restore() error {
	if atomic.LoadUint32(&r.Active) == 0 {
		return nil
	}
	hwnd := uintptr(r.HWND)
	property := captureRestoreProperty()
	token, _, _ := captureUser32.NewProc("GetPropW").Call(hwnd, uintptr(unsafe.Pointer(property)))
	if token != uintptr(r.Token) {
		return nil // destroyed/replaced window, or already cleaned up
	}
	var pid uint32
	captureUser32.NewProc("GetWindowThreadProcessId").Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid != r.PID {
		return nil
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return nil // the owner exited
	}
	defer windows.CloseHandle(process)
	created, err := captureProcessBirth(process)
	if err != nil || created != r.Created {
		return nil
	}
	minimized, _, _ := captureUser32.NewProc("IsIconic").Call(hwnd)
	if minimized == 0 {
		// A bounded minimize command acknowledges the state change before
		// removing transparency. ShowWindowAsync plus WM_NULL is insufficient:
		// WM_NULL can overtake its queued show event and expose a restored window.
		// Minimizing this inactive window does not activate it or another app.
		var result uintptr
		captureUser32.NewProc("SendMessageTimeoutW").Call(hwnd, 0x0112, 0xF020, 0, 0x0001|0x0002, 500, uintptr(unsafe.Pointer(&result))) // WM_SYSCOMMAND, SC_MINIMIZE
		minimized, _, _ = captureUser32.NewProc("IsIconic").Call(hwnd)
		if minimized == 0 {
			return fmt.Errorf("capture finished but the window owner did not acknowledge re-minimizing; temporary transparency retained to avoid exposing it")
		}
	}
	get, set := "GetWindowLongPtrW", "SetWindowLongPtrW"
	if unsafe.Sizeof(uintptr(0)) == 4 {
		get, set = "GetWindowLongW", "SetWindowLongW"
	}
	const exStyle = ^uintptr(19)
	const temporary = 0x00080000 | 0x00000020 // WS_EX_LAYERED | WS_EX_TRANSPARENT
	current, _, _ := captureUser32.NewProc(get).Call(hwnd, exStyle)
	// Preserve unrelated style changes made by the application during capture.
	captureUser32.NewProc(set).Call(hwnd, exStyle, current&^temporary|uintptr(r.OriginalStyle)&temporary)
	after, _, _ := captureUser32.NewProc(get).Call(hwnd, exStyle)
	if after&temporary != uintptr(r.OriginalStyle)&temporary {
		return fmt.Errorf("window was re-minimized but its owner refused restoring temporary capture styles")
	}
	captureUser32.NewProc("RemovePropW").Call(hwnd, uintptr(unsafe.Pointer(property)))
	atomic.StoreUint32(&r.Active, 0)
	return nil
}
