//go:build windows

package desktopfiles

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var pngFormatOnce sync.Once
var pngClipboardFormat uintptr

func clipboardImageFormats() []uintptr {
	pngFormatOnce.Do(func() {
		name, _ := syscall.UTF16PtrFromString("PNG")
		pngClipboardFormat, _, _ = user32Picker.NewProc("RegisterClipboardFormatW").Call(uintptr(unsafe.Pointer(name)))
	})
	return []uintptr{pngClipboardFormat, 8, 17}
}
func HasClipboardImage() bool {
	for _, format := range clipboardImageFormats() {
		if format == 0 {
			continue
		}
		if yes, _, _ := procIsClipboardFormatAvailable.Call(format); yes != 0 {
			return true
		}
	}
	return false
}
func ClipboardPNG() ([]byte, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ok, _, err := procOpenClipboard.Call(0)
	if ok == 0 {
		return nil, fmt.Errorf("open clipboard: %v", err)
	}
	var raw []byte
	var isPNG bool
	var lastErr error
	for _, format := range clipboardImageFormats() {
		if format == 0 {
			continue
		}
		handle, _, _ := procGetClipboardData.Call(format)
		if handle == 0 {
			continue
		}
		raw, lastErr = copyClipboardMemory(handle)
		if lastErr == nil {
			isPNG = format == pngClipboardFormat
			break
		}
	}
	procCloseClipboard.Call()
	if len(raw) == 0 {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("clipboard image unavailable")
	}
	if isPNG {
		return raw, validateClipboardPNG(raw)
	}
	return decodeClipboardDIB(raw)
}
func copyClipboardMemory(handle uintptr) ([]byte, error) {
	kernel := windows.NewLazySystemDLL("kernel32.dll")
	size, _, _ := kernel.NewProc("GlobalSize").Call(handle)
	if size == 0 || size > maxClipboardBytes {
		return nil, fmt.Errorf("clipboard image size is invalid")
	}
	ptr, _, err := kernel.NewProc("GlobalLock").Call(handle)
	if ptr == 0 {
		return nil, fmt.Errorf("lock clipboard image: %v", err)
	}
	defer kernel.NewProc("GlobalUnlock").Call(handle)
	raw := make([]byte, int(size))
	kernel.NewProc("RtlMoveMemory").Call(uintptr(unsafe.Pointer(&raw[0])), ptr, size)
	runtime.KeepAlive(raw)
	return raw, nil
}
