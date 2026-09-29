//go:build !windows

package updater

import (
	"fmt"
	"os"
	"syscall"
)

func lockFile(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("another update is running: %w", err)
	}
	return func() { syscall.Flock(int(file.Fd()), syscall.LOCK_UN); file.Close() }, nil
}
