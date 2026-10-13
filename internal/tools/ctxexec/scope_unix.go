//go:build unix

package ctxexec

import (
	"os/exec"
	"syscall"

	"supercli/internal/system/childproc"
)

func configureCommandScope(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// New session (also a new process group) without the TUI's terminal;
	// see childproc.detachSession.
	cmd.SysProcAttr.Setsid = true
}

func killCommandTree(cmd *exec.Cmd, scope *childproc.Scope) error {
	if cmd != nil && cmd.Process != nil && cmd.Process.Pid > 0 {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
			return nil
		}
	}
	return scope.Kill(cmd)
}
