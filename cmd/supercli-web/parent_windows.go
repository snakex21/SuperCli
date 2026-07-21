//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

func monitorParent(pid int) {
	if pid <= 0 {
		return
	}
	const synchronize = 0x00100000
	handle, err := windows.OpenProcess(synchronize, false, uint32(pid))
	if err != nil {
		return
	}
	go func() {
		defer windows.CloseHandle(handle)
		if _, err := windows.WaitForSingleObject(handle, windows.INFINITE); err == nil {
			os.Exit(0)
		}
	}()
}
