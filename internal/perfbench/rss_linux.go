//go:build linux

package perfbench

import (
	"fmt"
	"os"
)

func processRSSMB(pid int) float64 {
	var pages int64
	f, err := os.Open(fmt.Sprintf("/proc/%d/statm", pid))
	if err != nil {
		return 0
	}
	defer f.Close()
	if _, err := fmt.Fscan(f, new(int64), &pages); err != nil {
		return 0
	}
	return float64(pages*int64(os.Getpagesize())) / (1024 * 1024)
}
