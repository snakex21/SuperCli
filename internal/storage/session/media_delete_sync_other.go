//go:build !windows

package session

import (
	"errors"
	"os"
)

func syncMediaDeleteDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
