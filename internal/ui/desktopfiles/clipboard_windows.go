//go:build windows

package desktopfiles

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const cfHDrop = 15

var (
	procIsClipboardFormatAvailable = user32Picker.NewProc("IsClipboardFormatAvailable")
	procOpenClipboard              = user32Picker.NewProc("OpenClipboard")
	procCloseClipboard             = user32Picker.NewProc("CloseClipboard")
	procGetClipboardData           = user32Picker.NewProc("GetClipboardData")
	procDragQueryFileW             = windows.NewLazySystemDLL("shell32.dll").NewProc("DragQueryFileW")
)

// ClipboardFiles reads copied Explorer files without changing clipboard
// ownership or contents. A nil result lets callers use normal text paste.
func ClipboardFiles(limit int) ([]string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	available, _, _ := procIsClipboardFormatAvailable.Call(cfHDrop)
	if available == 0 {
		return nil, nil
	}
	ok, _, err := procOpenClipboard.Call(0)
	if ok == 0 {
		return nil, fmt.Errorf("open clipboard: %v", err)
	}
	defer procCloseClipboard.Call()
	handle, _, err := procGetClipboardData.Call(cfHDrop)
	if handle == 0 {
		return nil, fmt.Errorf("read clipboard files: %v", err)
	}
	// The handle belongs to Windows: do not free it or call DragFinish.
	return clipboardDropFiles(handle, limit)
}

func clipboardDropFiles(handle uintptr, limit int) ([]string, error) {
	count, _, _ := procDragQueryFileW.Call(handle, 0xffffffff, 0, 0)
	if count == 0 {
		return nil, fmt.Errorf("clipboard file list is empty")
	}
	if limit <= 0 || count > uintptr(limit) {
		return nil, fmt.Errorf("too many clipboard files: %d (maximum %d)", count, limit)
	}
	paths := make([]string, 0, int(count))
	for i := uintptr(0); i < count; i++ {
		length, _, _ := procDragQueryFileW.Call(handle, i, 0, 0)
		if length == 0 || length > 32767 {
			return nil, fmt.Errorf("invalid clipboard file path")
		}
		buffer := make([]uint16, int(length)+1)
		copied, _, _ := procDragQueryFileW.Call(handle, i, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
		if copied != length {
			return nil, fmt.Errorf("could not read clipboard file path")
		}
		paths = append(paths, syscall.UTF16ToString(buffer))
	}
	return paths, nil
}
