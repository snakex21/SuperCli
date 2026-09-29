//go:build windows

package updater

import (
	"fmt"
	"golang.org/x/sys/windows"
)

func lockFile(path string) (func(), error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, fmt.Errorf("another update is running or the update folder is unwritable: %w", err)
	}
	return func() { windows.CloseHandle(handle) }, nil
}
