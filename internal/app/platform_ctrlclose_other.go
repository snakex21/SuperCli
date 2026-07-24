//go:build !windows

package app

// installCloseHandler is a no-op outside Windows. On Unix an
// abrupt terminal close delivers SIGHUP, which already terminates
// the process through the normal signal path; the per-turn
// incremental memory save bounds the loss to the last turn.
func installCloseHandler(func()) {}
