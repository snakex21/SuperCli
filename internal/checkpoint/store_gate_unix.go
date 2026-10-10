//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package checkpoint

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func checkpointStoreLock(path string) (func() error, error) {
	// NOFOLLOW also protects against a leaf link created after Lstat. NONBLOCK
	// avoids waiting on a special file before the descriptor's type is checked.
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, fmt.Errorf("checkpoint store lock: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("checkpoint store lock: %w", err), file.Close())
	}
	if !info.Mode().IsRegular() {
		return nil, errors.Join(errors.New("checkpoint store lock must be a regular file, not a special file"), file.Close())
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		closeErr := file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, errors.Join(fmt.Errorf("%w: %w", ErrStoreBusy, err), closeErr)
		}
		return nil, errors.Join(fmt.Errorf("checkpoint store lock: %w", err), closeErr)
	}
	return func() error {
		return errors.Join(unix.Flock(fd, unix.LOCK_UN), file.Close())
	}, nil
}
