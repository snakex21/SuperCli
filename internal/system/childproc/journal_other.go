//go:build !windows

package childproc

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// Unix process identity: /proc/<pid>/stat field 22 (starttime, clock
// ticks since boot) when /proc exists; otherwise a signal-0 probe
// without the PID-reuse guard.
//
// starttime is only meaningful while the machine has not rebooted; a
// reboot makes every journaled stamp stale, and the sweep then sees
// the old pids as dead (which they are — the children died with the
// old kernel). Safe in both directions.

// startTimeFromProc reads /proc/<pid>/stat and returns field 22, or -1
// when the file is unreadable (process gone, no /proc, permission).
func startTimeFromProc(pid int) int64 {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return -1
	}
	s := string(data)
	// comm (field 2) may contain spaces and parens; the rest starts
	// after the LAST ')'.
	i := strings.LastIndexByte(s, ')')
	if i < 0 || i+2 > len(s) {
		return -1
	}
	fields := strings.Fields(s[i+2:]) // now field 3 == fields[0]
	if len(fields) < 20 {
		return -1
	}
	v, err := strconv.ParseInt(fields[19], 10, 64) // field 22 == index 19
	if err != nil {
		return -1
	}
	return v
}

// selfStartNS returns the creation stamp of the current process (or 0
// when /proc is unavailable).
func selfStartNS() int64 {
	return startTimeFromProc(os.Getpid())
}

// childStartNS returns the creation stamp of pid, or -1 when it cannot
// be determined.
func childStartNS(pid int) int64 {
	return startTimeFromProc(pid)
}

// alive reports whether pid exists and was created at startNS. With
// /proc the start-time guard is exact; without it (macOS etc.) a
// signal-0 probe is used and startNS is ignored.
func alive(pid int, startNS int64) bool {
	if pid <= 0 {
		return false
	}
	if st := startTimeFromProc(pid); st >= 0 {
		if startNS <= 0 {
			return true
		}
		return st == startNS
	}
	// No /proc: plain liveness probe, no reuse guard.
	return syscall.Kill(pid, 0) == nil
}

// terminate force-kills pid.
func terminate(pid int) error {
	return syscall.Kill(pid, syscall.SIGKILL)
}
