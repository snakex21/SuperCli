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
//
// It merges into any SysProcAttr the caller already set instead of replacing it.
// Replacing it was a trap: Start calls HideWindow, so a caller that had set
// SysProcAttr.CmdLine (shellescape, to hand cmd.exe a raw command line) silently
// lost it and fell back to Go's argv quoting. Merging keeps the two settings
// independent of call order.
func HideWindow(cmd *exec.Cmd) *exec.Cmd {
	if cmd == nil {
		return cmd
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
	return cmd
}

// NoConsoleWindow suppresses a console window without forcing the first GUI
// window created by the child process into SW_HIDE. Use this for helpers such
// as native file pickers that intentionally create their own visible window.
func NoConsoleWindow(cmd *exec.Cmd) *exec.Cmd {
	if cmd == nil {
		return cmd
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNoWindow
	return cmd
}
