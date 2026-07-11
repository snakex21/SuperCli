//go:build !windows

// Package childproc provides cross-platform child-process configuration.
package childproc

import "os/exec"

// HideWindow is a no-op where child console windows are not a Windows concern.
func HideWindow(cmd *exec.Cmd) *exec.Cmd { return cmd }
