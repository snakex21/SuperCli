//go:build !windows

package childproc

import "os/exec"

// startTree starts cmd as its own process group (not journaled).
func startTree(cmd *exec.Cmd) (*Scope, error) {
	detachSession(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Scope{}, nil
}

// killTree ends the process group, then the process.
func (*Scope) killTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	killGroup(cmd.Process.Pid)
	return cmd.Process.Kill()
}
