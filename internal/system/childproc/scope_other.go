//go:build !windows

package childproc

import "os/exec"

// Scope is the cross-platform lifetime handle for a started child process.
type Scope struct{}

// Start launches cmd and records the spawn in the orphan-process
// journal: without a Windows Job Object this journal is the ONLY
// cleanup path for children of a crashed process.
func Start(cmd *exec.Cmd) (*Scope, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	journalChild(cmd)
	return &Scope{}, nil
}

func (*Scope) Kill(cmd *exec.Cmd) error {
	journalDone(cmd)
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

func (*Scope) Close() error { return nil }
