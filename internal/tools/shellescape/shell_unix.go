//go:build unix

package shellescape

import (
	"context"
	"os/exec"
	"syscall"

	"supercli/internal/system/childproc"
)

// newShellCmd builds the child process that runs a shell-escape command.
//
// POSIX shells take the command as a single argv element, which exec passes
// through untouched, so no special handling is needed here. The Windows
// counterpart in shell_windows.go has to work around cmd.exe's quoting rules.
//
// Setpgid puts the shell in its own process group so the timeout can signal the
// whole group. Windows gets the same guarantee from a Job Object; POSIX has no
// job objects, and a process group is the closest equivalent that needs no extra
// bookkeeping. It is safe here because a shell-escape command is a one-shot
// non-interactive child: nothing forwards terminal signals to it, cancellation
// travels through the context.
func newShellCmd(ctx context.Context, command string) *exec.Cmd {
	c := exec.CommandContext(ctx, "sh", "-c", command)
	childproc.HideWindow(c)
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.Setpgid = true
	return c
}

// killShellTree signals the shell's whole process group, so children that
// inherited the stdout/stderr pipes die with it. Killing only the shell left
// those pipes open and Wait blocked on them until the children finished on
// their own, which is exactly the timeout that was not enforced.
//
// The group kill falls back to killing the shell alone if the group is gone
// (e.g. the child was reaped between the deadline and the signal).
func killShellTree(cmd *exec.Cmd, scope *childproc.Scope) error {
	if cmd != nil && cmd.Process != nil && cmd.Process.Pid > 0 {
		// Setpgid made the child a group leader, so its pgid equals its pid.
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
			return nil
		}
	}
	return scope.Kill(cmd)
}
