//go:build windows

package config

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// readConfigFile opens config.toml with FILE_SHARE_DELETE, so a concurrent
// save can atomically replace the file while it is being read (the reader
// keeps the old content). os.ReadFile omits that share mode on Windows.
func readConfigFile(path string) ([]byte, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(h), path)
	defer f.Close()
	return io.ReadAll(f)
}
