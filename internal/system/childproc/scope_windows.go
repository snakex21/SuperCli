//go:build windows

package childproc

import (
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Scope owns the Windows Job Object assigned to one child process. Closing the
// job terminates the complete descendant tree, including helpers spawned by an
// MCP or language server that would otherwise survive the parent process.
type Scope struct {
	mu  sync.Mutex
	job windows.Handle
}

// Start launches cmd and, where Windows permits it, places it in a kill-on-close
// Job Object. Job setup is best effort because some managed/older Windows hosts
// forbid nested jobs; direct process termination remains available as fallback.
func Start(cmd *exec.Cmd) (*Scope, error) {
	HideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	s := &Scope{}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return s, nil
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return s, nil
	}
	var assignErr error
	if err = cmd.Process.WithHandle(func(handle uintptr) {
		assignErr = windows.AssignProcessToJobObject(job, windows.Handle(handle))
	}); err != nil {
		assignErr = err
	}
	if assignErr != nil {
		_ = windows.CloseHandle(job)
		return s, nil
	}
	s.job = job
	return s, nil
}

// Kill terminates cmd and every descendant assigned to its job. It is safe to
// race with Wait/Close and safe to call more than once.
func (s *Scope) Kill(cmd *exec.Cmd) error {
	killedTree := false
	if s != nil {
		s.mu.Lock()
		if s.job != 0 {
			killedTree = windows.CloseHandle(s.job) == nil
			s.job = 0
		}
		s.mu.Unlock()
	}
	if killedTree {
		return nil
	}
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

// Close releases the job after the child has exited naturally.
func (s *Scope) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job == 0 {
		return nil
	}
	err := windows.CloseHandle(s.job)
	s.job = 0
	return err
}
