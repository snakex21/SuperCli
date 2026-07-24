//go:build !windows

package webgui

import "os"

func restrictTokenFile(path string) error {
	return os.Chmod(path, 0o600)
}
