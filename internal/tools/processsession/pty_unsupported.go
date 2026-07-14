//go:build !windows && !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !zos

package processsession

import "errors"

func startPTY(_ []string, _ string, _ []string, _, _ int) (ptyProcess, error) {
	return nil, errors.New("pseudo-terminals are not supported on this platform")
}
