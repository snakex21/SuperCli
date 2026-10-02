//go:build windows

package session

import "golang.org/x/sys/windows"

func replaceSessionImage(oldPath, newPath string, replace bool) error {
	oldPtr, err := windows.UTF16PtrFromString(oldPath)
	if err != nil {
		return err
	}
	newPtr, err := windows.UTF16PtrFromString(newPath)
	if err != nil {
		return err
	}
	// Same-directory rename: no copy/delete or truncate fallback. WRITE_THROUGH
	// is intentionally omitted; this publishes complete bytes, not power-loss fsync.
	var flags uint32
	if replace {
		flags = windows.MOVEFILE_REPLACE_EXISTING
	}
	return windows.MoveFileEx(oldPtr, newPtr, flags)
}
