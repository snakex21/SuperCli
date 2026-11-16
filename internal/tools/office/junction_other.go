//go:build !windows

package office

import "io/fs"

// isDirectoryJunction is always false outside Windows (no NTFS junctions).
func isDirectoryJunction(string, fs.FileInfo) bool { return false }
