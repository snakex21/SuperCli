//go:build !windows

package web

import "os"

// Linking a completed file never replaces a destination created concurrently.
// The caller removes the temporary sibling after publishing.
func publishDownload(from, to string) error {
	return os.Link(from, to)
}
