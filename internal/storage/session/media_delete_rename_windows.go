//go:build windows

package session

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func renameMediaDelete(from, to string) error {
	extended := func(path string) (string, error) {
		path, err := filepath.Abs(path)
		if err != nil || strings.HasPrefix(path, `\\?\`) {
			return path, err
		}
		if strings.HasPrefix(path, `\\`) {
			return `\\?\UNC\` + path[2:], nil
		}
		return `\\?\` + path, nil
	}
	from, err := extended(from)
	if err != nil {
		return err
	}
	to, err = extended(to)
	if err != nil {
		return err
	}
	old, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	newName, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	// No REPLACE_EXISTING: a new original is never overwritten during restore.
	return windows.MoveFileEx(old, newName, windows.MOVEFILE_WRITE_THROUGH)
}

func syncMediaDeleteDir(string) error { return nil }
