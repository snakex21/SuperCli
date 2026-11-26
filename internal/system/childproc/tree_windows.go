//go:build windows

package childproc

import (
	"os/exec"

	"golang.org/x/sys/windows"
)

// startTree starts cmd hidden and in a kill-on-close job (not journaled).
func startTree(cmd *exec.Cmd) (*Scope, error) {
	HideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	s := &Scope{}
	if err := cmd.Process.WithHandle(func(handle uintptr) {
		s.job = killOnCloseJob(windows.Handle(handle))
	}); err != nil {
		s.job = 0
	}
	return s, nil
}

// killTree ends the job (the whole tree), or the process when there is none.
func (s *Scope) killTree(cmd *exec.Cmd) error {
	s.mu.Lock()
	job := s.job
	s.job = 0
	s.mu.Unlock()
	if job != 0 && windows.CloseHandle(job) == nil {
		return nil
	}
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
