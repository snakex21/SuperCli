//go:build windows
// +build windows

package sandbox

import "os"

// On Windows os.Symlink requires admin privileges in
// older versions; tests that need symlinks skip via
// t.Skip. This wrapper is here so production code
// paths compile cleanly on both platforms.
func symlinkLink(oldname, newname string) error {
	return os.Symlink(oldname, newname)
}
