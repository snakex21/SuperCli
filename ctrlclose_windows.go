//go:build windows

package main

import (
	"syscall"
)

// Windows console control events (wincon.h).
const (
	ctrlCloseEvent    = 2 // user clicked the console window's X
	ctrlLogoffEvent   = 5
	ctrlShutdownEvent = 6
)

// installCloseHandler registers a SetConsoleCtrlHandler callback
// that runs fn when the console window is closed (or the user logs
// off / the system shuts down). Windows gives the handler ~5s
// before killing the process — enough for a local DB write, not
// for an LLM call. Ctrl+C / Ctrl+Break are NOT intercepted here
// (return FALSE → normal handling, bubbletea owns Ctrl+C).
//
// Blocking inside the callback is what keeps the process alive
// while fn runs, so fn is called synchronously.
func installCloseHandler(fn func()) {
	if fn == nil {
		return
	}
	handler := syscall.NewCallback(func(event uintptr) uintptr {
		switch event {
		case ctrlCloseEvent, ctrlLogoffEvent, ctrlShutdownEvent:
			fn()
			return 1 // handled
		}
		return 0 // pass Ctrl+C/Break to the next handler
	})
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	setHandler := kernel32.NewProc("SetConsoleCtrlHandler")
	_, _, _ = setHandler.Call(handler, 1)
}
