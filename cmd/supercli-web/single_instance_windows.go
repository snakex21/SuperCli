//go:build windows

package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"

	"supercli/internal/system/uilang"
)

var singleInstanceMessageBox = windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW")

func claimSingleInstance(profile string) (func(), bool, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, false, fmt.Errorf("resolve executable: %w", err)
	}
	name, err := windows.UTF16PtrFromString(singleInstanceMutexName(profile, executable))
	if err != nil {
		return nil, false, err
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return nil, false, fmt.Errorf("create mutex: %w", err)
	}
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
		return func() {}, true, nil
	}
	return func() { _ = windows.CloseHandle(handle) }, false, nil
}

func singleInstanceMutexName(profile, executable string) string {
	profile = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, strings.TrimSpace(profile))
	if profile == "" {
		profile = "supercli"
	}
	if absolute, err := filepath.Abs(executable); err == nil {
		executable = absolute
	}
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(executable))))
	return fmt.Sprintf(`Local\SuperCli.Desktop.%s.%x`, profile, sum[:12])
}

// There is no browser here to translate for us, so the two variants live in
// Go — the only place a native message box can read them from.
func notifyAlreadyRunning(appName, language string) {
	notice := fmt.Sprintf("%s is already running.", appName)
	if uilang.IsPolish(language) {
		notice = fmt.Sprintf("%s jest już uruchomione.", appName)
	}
	text, textErr := windows.UTF16PtrFromString(notice)
	title, titleErr := windows.UTF16PtrFromString(appName)
	if textErr != nil || titleErr != nil {
		return
	}
	const mbOKIconInformation = 0x00000040
	_, _, _ = singleInstanceMessageBox.Call(
		0,
		uintptr(unsafe.Pointer(text)),
		uintptr(unsafe.Pointer(title)),
		mbOKIconInformation,
	)
}
