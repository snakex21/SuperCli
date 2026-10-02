//go:build !windows

package session

import "os"

func replaceSessionImage(oldPath, newPath string, _ bool) error { return os.Rename(oldPath, newPath) }
