//go:build !windows

package ctxexec

func isNativeConnectionReset(error) bool { return false }
