//go:build unix

package fileops

import (
	"os"
	"syscall"
)

// fileLinkCount returns the number of hard links to path.
func fileLinkCount(path string) (uint32, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint32(st.Nlink), true
}
