//go:build windows

package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestResolveCanonicalJunctionHome(t *testing.T) {
	root := t.TempDir()
	realHome, alias := filepath.Join(root, "real"), filepath.Join(root, "junction")
	if err := os.Mkdir(realHome, 0700); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("cmd", "/c", "mklink", "/J", alias, realHome).CombinedOutput(); err != nil {
		t.Skipf("junction creation unavailable: %v (%s)", err, output)
	}
	t.Cleanup(func() { _ = os.Remove(alias) })
	verifyCanonicalHome(t, alias)
}

func TestResolveCanonicalShortNameHome(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	long, err := windows.UTF16PtrFromString(home)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, 32768)
	n, err := windows.GetShortPathName(long, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || n >= uint32(len(buf)) {
		t.Skipf("8.3 path unavailable: length=%d error=%v", n, err)
	}
	short := windows.UTF16ToString(buf[:n])
	if strings.EqualFold(short, home) {
		t.Skip("filesystem does not expose an alternate 8.3 path")
	}
	verifyCanonicalHome(t, short)
}
