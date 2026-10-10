//go:build windows

package checkpoint

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func markMetadataSparseFixture(t *testing.T, f *os.File) {
	t.Helper()
	var returned uint32
	if err := windows.DeviceIoControl(windows.Handle(f.Fd()), windows.FSCTL_SET_SPARSE, nil, 0, nil, 0, &returned, nil); err != nil {
		t.Skip("synthetic filesystem does not support sparse metadata:", err)
	}
}
