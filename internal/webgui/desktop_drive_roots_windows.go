//go:build windows

package webgui

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func logicalDriveRoots() []string {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil
	}
	roots := make([]string, 0, 4)
	for index := uint32(0); index < 26; index++ {
		if mask&(1<<index) == 0 {
			continue
		}
		roots = append(roots, fmt.Sprintf("%c:\\", 'A'+index))
	}
	return roots
}
