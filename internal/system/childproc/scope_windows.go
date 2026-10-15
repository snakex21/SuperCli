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
// The spawn is also recorded in the orphan-process journal, which covers the
// cases a job object cannot: a hard crash never runs Stop, and nested-job hosts
// never get the job at all.
func Start(cmd *exec.Cmd) (*Scope, error) {
	HideWindow(cmd)
	return startScoped(cmd)
}

// StartVisible is Start without forcing the child's first GUI window into
// SW_HIDE. Console programs still get no console window; GUI programs show
// their window normally, so it can be captured or inspected.
func StartVisible(cmd *exec.Cmd) (*Scope, error) {
	NoConsoleWindow(cmd)
	return startScoped(cmd)
}

func startScoped(cmd *exec.Cmd) (*Scope, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	journalChild(cmd)
	s := &Scope{}
	if err := cmd.Process.WithHandle(func(handle uintptr) {
		s.job = killOnCloseJob(windows.Handle(handle))
	}); err != nil {
		s.job = 0
	}
	return s, nil
}

// killOnCloseJob returns a kill-on-close job holding process, or 0 when the
// host forbids it.
func killOnCloseJob(process windows.Handle) windows.Handle {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0
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
		return 0
	}
	if err = windows.AssignProcessToJobObject(job, process); err != nil {
		_ = windows.CloseHandle(job)
		return 0
	}
	return job
}

// Kill terminates cmd and every descendant assigned to its job. It is safe to
// race with Wait/Close and safe to call more than once.
func (s *Scope) Kill(cmd *exec.Cmd) error {
	journalDone(cmd)
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
