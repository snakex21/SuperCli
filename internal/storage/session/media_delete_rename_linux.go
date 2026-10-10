//go:build linux

package session

import (
	"path/filepath"

	"golang.org/x/sys/unix"
)

func renameMediaDelete(from, to string) error {
	// Restore must not replace a directory another publisher created between
	// the absence check and this atomic rename. Unsupported filesystems fail.
	if err := unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE); err != nil {
		return err
	}
	if err := syncMediaDeleteDir(filepath.Dir(to)); err != nil {
		return err
	}
	if filepath.Dir(from) != filepath.Dir(to) {
		return syncMediaDeleteDir(filepath.Dir(from))
	}
	return nil
}
