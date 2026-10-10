//go:build !windows && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package checkpoint

import (
	"fmt"
	"runtime"
)

// Do not silently substitute an in-process mutex or a stale PID file on an OS
// without the native lock used by this store. All released platforms have one.
func checkpointStoreLock(string) (func() error, error) {
	return nil, fmt.Errorf("%w: %s", ErrStoreUnsupported, runtime.GOOS)
}
