//go:build windows

package processoutput

import (
	"golang.org/x/sys/windows"
	"unicode/utf16"
	"unsafe"
)

var kernel = windows.NewLazySystemDLL("kernel32.dll")
var oemCP = kernel.NewProc("GetOEMCP")
var ansiCP = kernel.NewProc("GetACP")
var multiByte = kernel.NewProc("MultiByteToWideChar")

func legacyText(p []byte) string {
	// Owned pipe commands use CREATE_NO_WINDOW. Their legacy fallback is
	// system OEM, not the parent's UTF-8 console setting. UTF-8 was checked first.
	cp, _, _ := oemCP.Call()
	if cp == 0 {
		cp, _, _ = ansiCP.Call()
	}
	return codePageText(p, cp)
}

func codePageText(p []byte, cp uintptr) string {
	if len(p) == 0 {
		return ""
	}
	n, _, _ := multiByte.Call(cp, 0, uintptr(unsafe.Pointer(&p[0])), uintptr(len(p)), 0, 0)
	if n == 0 {
		return validText(p)
	}
	out := make([]uint16, n)
	got, _, _ := multiByte.Call(cp, 0, uintptr(unsafe.Pointer(&p[0])), uintptr(len(p)), uintptr(unsafe.Pointer(&out[0])), n)
	if got == 0 {
		return validText(p)
	}
	return string(utf16.Decode(out[:got]))
}
