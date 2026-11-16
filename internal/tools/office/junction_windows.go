//go:build windows

package office

import (
	"io/fs"

	"golang.org/x/sys/windows"
)

// isDirectoryJunction reports whether path is an NTFS directory junction
// (an IO_REPARSE_TAG_MOUNT_POINT reparse point on a directory). Since Go
// 1.23 os.Lstat reports such a path as ModeIrregular rather than a symlink.
func isDirectoryJunction(path string, info fs.FileInfo) bool {
	if info.Mode()&fs.ModeSymlink != 0 || info.Mode()&fs.ModeIrregular == 0 {
		return false
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	var data windows.Win32finddata
	h, err := windows.FindFirstFile(p, &data)
	if err != nil {
		return false
	}
	_ = windows.FindClose(h)
	return data.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 &&
		data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 &&
		data.Reserved0 == windows.IO_REPARSE_TAG_MOUNT_POINT
}
