//go:build windows

package fileops

import (
	"os"
	"syscall"
)

// fileLinkCount returns the number of hard links to path.
func fileLinkCount(path string) (uint32, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &info); err != nil {
		return 0, false
	}
	return info.NumberOfLinks, true
}
