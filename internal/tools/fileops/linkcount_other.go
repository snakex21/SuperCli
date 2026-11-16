//go:build !windows && !unix

package fileops

// fileLinkCount is unknown on this platform, so the in-place fallback of
// ReplaceFileContents is never taken.
func fileLinkCount(string) (uint32, bool) { return 0, false }
