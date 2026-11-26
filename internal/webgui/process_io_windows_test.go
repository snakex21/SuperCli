//go:build windows

package webgui

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var procGetProcessIoCounters = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessIoCounters")

// processReadBytes is this process's total bytes read (all I/O), for tests.
func processReadBytes() int64 {
	var counters struct {
		ReadOperationCount, WriteOperationCount, OtherOperationCount uint64
		ReadTransferCount, WriteTransferCount, OtherTransferCount    uint64
	}
	r, _, _ := procGetProcessIoCounters.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&counters)))
	if r == 0 {
		return 0
	}
	return int64(counters.ReadTransferCount)
}
