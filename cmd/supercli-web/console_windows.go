//go:build windows

package main

import (
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

var freeConsole = windows.NewLazySystemDLL("kernel32.dll").NewProc("FreeConsole")

// detachAccidentalConsole is a safety net for development builds that omitted
// the required -H windowsgui linker flag. FreeConsole closes the extra console
// allocated by Explorer without hiding a parent PowerShell window. Server-only
// and help modes retain their console intentionally.
func detachAccidentalConsole() {
	for _, raw := range os.Args[1:] {
		arg := strings.ToLower(strings.TrimSpace(raw))
		if key, _, found := strings.Cut(arg, "="); found {
			arg = key
		}
		switch arg {
		case "--no-window", "-no-window", "-h", "--help", "/?":
			return
		}
	}
	_, _, _ = freeConsole.Call()
}
