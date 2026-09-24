//go:build windows

package desktopfiles

import (
	"encoding/binary"
	"reflect"
	"runtime"
	"testing"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Exercise the real Shell32 decoder with a DROPFILES fixture without reading
// or changing the user's clipboard.
func TestClipboardDropFilesUnicodeAndLimits(t *testing.T) {
	paths := []string{"C:\\Zdjęcia\\żółć ą.png", "D:\\obrazy\\test.jpg"}
	data := make([]byte, 20)
	binary.LittleEndian.PutUint32(data[0:], 20) // DROPFILES.pFiles
	binary.LittleEndian.PutUint32(data[16:], 1) // DROPFILES.fWide
	for _, path := range paths {
		for _, u := range append(utf16.Encode([]rune(path)), 0) {
			data = append(data, byte(u), byte(u>>8))
		}
	}
	data = append(data, 0, 0)
	dll := windows.NewLazySystemDLL("kernel32.dll")
	alloc := dll.NewProc("GlobalAlloc")
	lock := dll.NewProc("GlobalLock")
	unlock := dll.NewProc("GlobalUnlock")
	free := dll.NewProc("GlobalFree")
	handle, _, err := alloc.Call(0x42, uintptr(len(data))) // GMEM_MOVEABLE | GMEM_ZEROINIT
	if handle == 0 {
		t.Fatal(err)
	}
	defer free.Call(handle)
	ptr, _, err := lock.Call(handle)
	if ptr == 0 {
		t.Fatal(err)
	}
	dll.NewProc("RtlMoveMemory").Call(ptr, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)))
	runtime.KeepAlive(data)
	unlock.Call(handle)
	got, err := clipboardDropFiles(handle, 8)
	if err != nil || !reflect.DeepEqual(got, paths) {
		t.Fatalf("got %#v: %v", got, err)
	}
	if _, err := clipboardDropFiles(handle, 1); err == nil {
		t.Fatal("ignored file limit")
	}
}
