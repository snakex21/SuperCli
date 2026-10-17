//go:build !windows && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package maintenance

import (
	"errors"
	"os"
)

func lockActivityFile(file *os.File, exclusive bool) (func(), error) {
	return nil, errors.New("portable housekeeping locking unsupported")
}
