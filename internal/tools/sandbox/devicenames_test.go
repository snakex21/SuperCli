package sandbox

import (
	"errors"
	"path/filepath"
	"runtime"
	"testing"
)

func TestIsReservedDeviceName(t *testing.T) {
	for _, name := range []string{
		"CON", "con", "Prn", "AUX", "NUL", "nul.txt", "NUL.tar.gz", "CON.", "CON ", "CON. .",
		"Con .log", "com1", "COM9", "LPT1", "lpt9.txt", "COM¹", "lpt³.x", "CONIN$", "conout$.txt", "aux:stream",
	} {
		if !isReservedDeviceName(name) {
			t.Errorf("isReservedDeviceName(%q) = false, want true", name)
		}
	}
	for _, name := range []string{
		"", ".", "..", "CONSOLE", "con1", "COM0", "COM10", "LPT", "nullable.txt", "auxiliary", "x.CON", "my-nul", ".nul", "COM⁴",
	} {
		if isReservedDeviceName(name) {
			t.Errorf("isReservedDeviceName(%q) = true, want false", name)
		}
	}
}

func TestResolveSafeReservedDeviceNames(t *testing.T) {
	prior := IsUnsandboxed()
	t.Cleanup(func() { SetUnsandboxed(prior) })
	ws := t.TempDir()
	names := []string{"CON", "nul.txt", filepath.Join("sub", "AUX.txt"), "COM1", "LPT²", "CONIN$", "con. ", filepath.Join("PRN", "x.txt")}
	for _, unsandboxed := range []bool{false, true} {
		SetUnsandboxed(unsandboxed)
		for _, name := range names {
			_, err := ResolveSafe(ws, name)
			if runtime.GOOS != "windows" {
				if errors.Is(err, ErrDenied) {
					t.Errorf("ResolveSafe(%q) refused on %s, where it is an ordinary file name: %v", name, runtime.GOOS, err)
				}
				continue
			}
			if !errors.Is(err, ErrDenied) {
				t.Errorf("ResolveSafe(%q) (unsandboxed=%v) err = %v, want ErrDenied", name, unsandboxed, err)
			}
		}
		if _, err := ResolveSafe(ws, "console.txt"); err != nil {
			t.Errorf("ResolveSafe(console.txt) refused: %v", err)
		}
	}
	if runtime.GOOS == "windows" {
		SetUnsandboxed(true)
		for _, p := range []string{`\\.\NUL`, `\\.\COM1`, `\\?\COM1`,filepath.Join(ws, "sub", "nul")} {
			if _, err := ResolveSafe(ws, p); !errors.Is(err, ErrDenied) {
				t.Errorf("ResolveSafe(%q) err = %v, want ErrDenied", p, err)
			}
		}
	}
}
