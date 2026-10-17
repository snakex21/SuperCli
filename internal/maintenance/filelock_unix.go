//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package maintenance

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

func lockActivityFile(file *os.File, exclusive bool) (func(), error) {
	flags := unix.LOCK_SH | unix.LOCK_NB
	if exclusive {
		flags = unix.LOCK_EX | unix.LOCK_NB
	}
	err := unix.Flock(int(file.Fd()), flags)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return nil, errActivityBusy
	}
	if err != nil {
		return nil, err
	}
	return func() { _ = unix.Flock(int(file.Fd()), unix.LOCK_UN) }, nil
}
