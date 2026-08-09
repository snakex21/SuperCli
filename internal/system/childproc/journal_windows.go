//go:build windows

package childproc

import (
	"golang.org/x/sys/windows"
)

// Windows process identity is anchored on creation time: GetProcessTimes
// works for any accessible pid, so the start-time guard against PID
// reuse is exact (no /proc needed).
const (
	// epochFiletime is the Unix epoch (1970-01-01) in FILETIME units
	// (100ns intervals since 1601-01-01).
	epochFiletime = int64(11644473600) * 10000000
	// processQueryAccess is enough to read process times without
	// touching the process.
	processQueryAccess = windows.PROCESS_QUERY_LIMITED_INFORMATION
	processTermAccess  = windows.PROCESS_TERMINATE
)

func filetimeNS(ft windows.Filetime) int64 {
	raw := int64(ft.HighDateTime)<<32 | int64(ft.LowDateTime)
	// Subtract the epoch BEFORE scaling so the multiply cannot
	// overflow int64 for any date after 1601.
	return (raw - epochFiletime) * 100
}

// selfStartNS returns the creation stamp of the current process.
func selfStartNS() int64 {
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &creation, &exit, &kernel, &user); err != nil {
		return 0
	}
	return filetimeNS(creation)
}

// childStartNS returns the creation stamp of pid, or -1 when the
// process is not accessible (including "does not exist").
func childStartNS(pid int) int64 {
	h, err := windows.OpenProcess(processQueryAccess, false, uint32(pid))
	if err != nil {
		return -1
	}
	defer windows.CloseHandle(h)
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return -1
	}
	return filetimeNS(creation)
}

// alive reports whether pid still exists and was created at startNS.
// A zero/negative stamp is treated as "no stamp available": the pid
// must then simply be openable (the reuse guard degrades gracefully).
func alive(pid int, startNS int64) bool {
	if pid <= 0 {
		return false
	}
	stamp := childStartNS(pid)
	if stamp < 0 {
		return false
	}
	if startNS <= 0 {
		return true
	}
	return stamp == startNS
}

// terminate force-kills pid.
func terminate(pid int) error {
	h, err := windows.OpenProcess(processTermAccess, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.TerminateProcess(h, 1)
}
