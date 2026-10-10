//go:build windows

package checkpoint

import (
	"strings"

	"golang.org/x/sys/windows"
)

func retentionPromote(from, to string) error {
	extended := func(path string) string {
		if strings.HasPrefix(path, `\\?\`) {
			return path
		}
		if strings.HasPrefix(path, `\\`) {
			return `\\?\UNC\` + path[2:]
		}
		return `\\?\` + path
	}
	old, err := windows.UTF16PtrFromString(extended(from))
	if err != nil {
		return err
	}
	newName, err := windows.UTF16PtrFromString(extended(to))
	if err != nil {
		return err
	}
	return windows.MoveFileEx(old, newName, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
