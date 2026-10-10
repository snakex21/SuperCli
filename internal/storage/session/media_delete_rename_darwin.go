//go:build darwin

package session

import (
	"path/filepath"

	"golang.org/x/sys/unix"
)

func renameMediaDelete(from, to string) error {
	if err := unix.RenamexNp(from, to, unix.RENAME_EXCL); err != nil {
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
