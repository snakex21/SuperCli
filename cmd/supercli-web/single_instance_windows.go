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

var (
	singleInstanceMessageBox = windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW")
	allowSetForeground       = windows.NewLazySystemDLL("user32.dll").NewProc("AllowSetForegroundWindow")
)

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

// showEventName is the event a running instance waits on to reveal its
// window, including one hidden while work finishes in the background.
func showEventName(profile string) string {
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	return singleInstanceMutexName(profile, executable) + ".Show"
}

// showExistingInstance asks the running instance to bring its window back.
// It reports false when that instance does not listen for the request.
func showExistingInstance(profile string) bool {
	name := showEventName(profile)
	if name == "" {
		return false
	}
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return false
	}
	event, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, namePtr)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(event)
	// This launch came from the user, so it may hand the foreground to the
	// running instance; otherwise Windows only flashes its taskbar button.
	const asfwAny = ^uintptr(0)
	_, _, _ = allowSetForeground.Call(asfwAny)
	return windows.SetEvent(event) == nil
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
