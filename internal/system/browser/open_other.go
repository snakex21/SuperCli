//go:build !windows && !linux && !darwin

package browser

import (
	"fmt"
	"runtime"
)

func openURL(string) error {
	return fmt.Errorf("opening the default browser is unsupported on %s", runtime.GOOS)
}
