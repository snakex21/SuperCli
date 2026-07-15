//go:build !windows

package childproc

import "os/exec"

// Scope is the cross-platform lifetime handle for a started child process.
type Scope struct{}

func Start(cmd *exec.Cmd) (*Scope, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Scope{}, nil
}

func (*Scope) Kill(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

func (*Scope) Close() error { return nil }
