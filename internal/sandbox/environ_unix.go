//go:build !windows
// +build !windows

package sandbox

import "os"

func environ() []string {
	return os.Environ()
}
