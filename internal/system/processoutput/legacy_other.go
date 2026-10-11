//go:build !windows

package processoutput

func legacyText(p []byte) string { return validText(p) }
