package sandbox

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// reservedDeviceNames are the DOS device names Windows resolves in ANY
// directory: "<ws>\CON" or "<ws>\sub\nul.txt" opens the console or the null
// device, not a file in the workspace (reads of CON/CONIN$ block the tool).
var reservedDeviceNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"CONIN$": true, "CONOUT$": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"COM\u00b9": true, "COM\u00b2": true, "COM\u00b3": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
	"LPT\u00b9": true, "LPT\u00b2": true, "LPT\u00b3": true,
}

// isReservedDeviceName reports whether one path component names a DOS
// device the way Windows parses it: case-insensitive, trailing dots and
// spaces ignored, and any extension (or ":stream") ignored, so "nul.txt",
// "Con .log", "AUX." and "com1:x" are all devices.
func isReservedDeviceName(component string) bool {
	name := strings.TrimRight(component, ". ")
	if i := strings.IndexAny(name, ".:"); i >= 0 {
		name = name[:i]
	}
	name = strings.TrimRight(name, " ")
	if name == "" {
		return false
	}
	return reservedDeviceNames[strings.ToUpper(name)]
}

// rejectDeviceNames refuses, on Windows, a path that names a reserved DOS
// device in any component checked: the part of abs below home when abs is
// inside home (the workspace path itself is the owner's choice), every
// component otherwise, and the device-namespace volume (\\.\NUL) form.
// Other platforms have no such names, so "con.txt" stays an ordinary file.
func rejectDeviceNames(home, abs string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	vol := filepath.VolumeName(abs)
	if len(vol) > 4 && (vol[0] == '\\' || vol[0] == '/') {
		// \\.\NUL, \\?\COM1, \??\CON: the device is the volume itself.
		if isReservedDeviceName(vol[4:]) {
			return deviceNameError(vol[4:])
		}
	}
	rest := abs[len(vol):]
	if IsUnder(home, abs) {
		if rel, err := filepath.Rel(home, abs); err == nil {
			rest = rel
		}
	}
	for _, comp := range strings.FieldsFunc(rest, func(r rune) bool { return r == '\\' || r == '/' }) {
		if isReservedDeviceName(comp) {
			return deviceNameError(comp)
		}
	}
	return nil
}

func deviceNameError(component string) error {
	return fmt.Errorf("%w: %q is a reserved Windows device name, not a file", ErrDenied, component)
}
