//go:build !windows

package webgui

// processReadBytes is unknown here (the read-size check then passes); the
// timing check still applies.
func processReadBytes() int64 { return 0 }
