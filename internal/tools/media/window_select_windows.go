//go:build windows

package media

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// Read-only window state queries; nothing here activates or focuses a window.
func init() {
	windowSelectionDetails = func(hwnd uintptr) windowSelectionState {
		enabled, _, _ := captureUser32.NewProc("IsWindowEnabled").Call(hwnd)
		owner, _, _ := captureUser32.NewProc("GetWindow").Call(hwnd, 4) // GW_OWNER
		class := make([]uint16, 64)
		captureUser32.NewProc("GetClassNameW").Call(hwnd, uintptr(unsafe.Pointer(&class[0])), uintptr(len(class)))
		return windowSelectionState{Enabled: enabled != 0, Owner: owner, Menu: windows.UTF16ToString(class) == "#32768"}
	}
}
