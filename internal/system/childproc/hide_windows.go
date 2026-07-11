//go:build windows

// Package childproc centralizes Windows child-process startup settings used by
// the GUI and its tools. Console programs started by a windowsgui binary would
// otherwise create a short-lived visible console window.
package childproc

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

// HideWindow prevents a console child from flashing a window on Windows.
func HideWindow(cmd *exec.Cmd) *exec.Cmd {
	if cmd == nil {
		return cmd
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
	return cmd
}
