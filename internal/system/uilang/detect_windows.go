//go:build windows

package uilang

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var getUserDefaultUILanguage = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetUserDefaultUILanguage")
var lcidToLocaleName = windows.NewLazySystemDLL("kernel32.dll").NewProc("LCIDToLocaleName")

func detectSystem() string {
	langID, _, callErr := getUserDefaultUILanguage.Call()
	if langID == 0 || callErr != windows.ERROR_SUCCESS {
		return ""
	}
	// Preserve the actual UI language's region/script when converting it.
	var name [85]uint16 // LOCALE_NAME_MAX_LENGTH
	length, _, err := lcidToLocaleName.Call(langID, uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)), 0)
	if length == 0 || err != windows.ERROR_SUCCESS {
		return ""
	}
	return Normalize(windows.UTF16ToString(name[:]))
}
