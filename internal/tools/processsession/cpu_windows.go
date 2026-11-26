//go:build windows

package processsession

import (
	"time"

	"golang.org/x/sys/windows"
)

// processCPUTime is the kernel+user CPU time the process has used so far.
func processCPUTime(pid int) (time.Duration, bool) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0, false
	}
	defer windows.CloseHandle(h)
	var created, exited, kernel, user windows.Filetime
	if windows.GetProcessTimes(h, &created, &exited, &kernel, &user) != nil {
		return 0, false
	}
	ticks := func(ft windows.Filetime) int64 { return int64(ft.HighDateTime)<<32 | int64(ft.LowDateTime) }
	return time.Duration(ticks(kernel)+ticks(user)) * 100, true
}
