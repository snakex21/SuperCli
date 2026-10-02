//go:build windows

package browser

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var shellExecuteExW = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

// Field order and native alignment match SHELLEXECUTEINFOW on both 32- and
// 64-bit Windows. No process handle is requested or retained.
type shellExecuteInfo struct {
	size, mask            uint32
	window                windows.Handle
	verb, file            *uint16
	parameters, directory *uint16
	show                  int32
	instance              windows.Handle
	idList                uintptr
	class                 *uint16
	classKey              windows.Handle
	hotKey                uint32
	icon, process         windows.Handle
}

func openURL(normalized string) error {
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	target, err := windows.UTF16PtrFromString(normalized)
	if err != nil {
		return err
	}
	// A shell association can invoke a COM extension. Keep its initialization,
	// launch and cleanup on one OS thread, independent of the UI's apartment.
	done := make(chan error, 1)
	go func() { done <- openURLOnThread(verb, target) }()
	return <-done
}

func openURLOnThread(verb, target *uint16) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	initErr := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE)
	// S_FALSE also owns an initialization reference that must be released.
	if initErr != nil && initErr != syscall.Errno(windows.S_FALSE) {
		return fmt.Errorf("open default browser: COM initialization (0x%08X): %w", uint32(initErr.(syscall.Errno)), initErr)
	}
	defer windows.CoUninitialize()
	info := shellExecuteInfo{
		mask: 0x00000100 | 0x00000400, // SEE_MASK_NOASYNC | SEE_MASK_FLAG_NO_UI
		verb: verb, file: target, show: windows.SW_SHOWNORMAL,
	}
	info.size = uint32(unsafe.Sizeof(info))
	// This worker has no message pump. Finish shell handoff before releasing
	// its apartment; report errors in the app instead of a shell error dialog.
	ok, _, callErr := shellExecuteExW.Call(uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		if callErr == syscall.Errno(0) {
			return fmt.Errorf("open default browser: ShellExecuteExW failed (shell code %d)", info.instance)
		} else {
			return fmt.Errorf("open default browser: ShellExecuteExW: %w", callErr)
		}
	}
	return nil
}
