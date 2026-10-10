//go:build windows

package web

import "golang.org/x/sys/windows"

// Same-folder MoveFileEx without REPLACE_EXISTING publishes the completed
// file atomically, refuses races with existing paths, and works on FAT/NTFS.
func publishDownload(from, to string) error {
	fromPtr, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	toPtr, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(fromPtr, toPtr, windows.MOVEFILE_WRITE_THROUGH)
}
