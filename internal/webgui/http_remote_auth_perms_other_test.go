//go:build !windows

package webgui

import (
	"os"
	"testing"
)

func assertTokenFileRestricted(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("token file mode=%#o, want 0600", got)
	}
}
