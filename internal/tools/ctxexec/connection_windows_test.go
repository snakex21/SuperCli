//go:build windows

package ctxexec

import (
	"errors"
	"syscall"
)

func isNativeConnectionReset(err error) bool { return errors.Is(err, syscall.WSAECONNRESET) }
