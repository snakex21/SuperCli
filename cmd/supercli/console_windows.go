//go:build windows

package main

import (
	"golang.org/x/sys/windows"
	"os"
)

// Settings apply only to the attached console, never to the registry or user
// profile. Restore the caller's console settings when the CLI exits normally.
func setupConsole() func() {
	kernel := windows.NewLazySystemDLL("kernel32.dll")
	user := windows.NewLazySystemDLL("user32.dll")
	getCP, setCP := kernel.NewProc("GetConsoleCP"), kernel.NewProc("SetConsoleCP")
	getOut, setOut := kernel.NewProc("GetConsoleOutputCP"), kernel.NewProc("SetConsoleOutputCP")
	input, _, _ := getCP.Call()
	output, _, _ := getOut.Call()
	if input != 0 {
		_, _, _ = setCP.Call(65001)
	}
	if output != 0 {
		_, _, _ = setOut.Call(65001)
	}
	window, _, _ := kernel.NewProc("GetConsoleWindow").Call()
	var oldSmall, oldBig uintptr
	send := user.NewProc("SendMessageW")
	changed := false
	if window != 0 && os.Getenv("WT_SESSION") == "" {
		module, _, _ := kernel.NewProc("GetModuleHandleW").Call(0)
		icon, _, _ := user.NewProc("LoadImageW").Call(module, 1, 1, 0, 0, 0x0040|0x8000)
		if icon != 0 {
			oldSmall, _, _ = send.Call(window, 0x0080, 0, icon)
			oldBig, _, _ = send.Call(window, 0x0080, 1, icon)
			changed = true
		}
	}
	return func() {
		if changed {
			_, _, _ = send.Call(window, 0x0080, 0, oldSmall)
			_, _, _ = send.Call(window, 0x0080, 1, oldBig)
		}
		if input != 0 {
			_, _, _ = setCP.Call(input)
		}
		if output != 0 {
			_, _, _ = setOut.Call(output)
		}
	}
}
