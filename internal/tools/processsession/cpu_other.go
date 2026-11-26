//go:build !windows

package processsession

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// processCPUTime is the user+system CPU time the process has used so far
// (Linux /proc; elsewhere unknown, and the hang check uses output only).
func processCPUTime(pid int) (time.Duration, bool) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	// Fields after the parenthesised command name; utime and stime are the
	// 14th and 15th fields of the whole line.
	text := string(raw)
	if i := strings.LastIndexByte(text, ')'); i >= 0 {
		text = text[i+1:]
	}
	fields := strings.Fields(text)
	if len(fields) < 13 {
		return 0, false
	}
	utime, err1 := strconv.ParseInt(fields[11], 10, 64)
	stime, err2 := strconv.ParseInt(fields[12], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	const clockTicks = 100 // USER_HZ on Linux
	return time.Duration(utime+stime) * time.Second / clockTicks, true
}
