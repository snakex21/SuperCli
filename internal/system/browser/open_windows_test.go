//go:build windows

package browser

import (
	"testing"
	"unsafe"
)

// A wrong ABI layout can silently turn a valid launch into a native failure.
func TestShellExecuteInfoNativeLayout(t *testing.T) {
	var info shellExecuteInfo
	pointerSize := unsafe.Sizeof(uintptr(0))
	wantSize, wantShow, wantInstance, wantProcess := uintptr(112), uintptr(48), uintptr(56), uintptr(104)
	if pointerSize == 4 {
		wantSize, wantShow, wantInstance, wantProcess = 60, 28, 32, 56
	}
	if unsafe.Sizeof(info) != wantSize || unsafe.Offsetof(info.show) != wantShow || unsafe.Offsetof(info.instance) != wantInstance || unsafe.Offsetof(info.process) != wantProcess {
		t.Fatalf("native SHELLEXECUTEINFOW layout: size=%d show=%d instance=%d process=%d, pointer=%d", unsafe.Sizeof(info), unsafe.Offsetof(info.show), unsafe.Offsetof(info.instance), unsafe.Offsetof(info.process), pointerSize)
	}
}
