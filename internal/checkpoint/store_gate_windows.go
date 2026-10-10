//go:build windows

package checkpoint

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

func checkpointStoreLock(path string) (func() error, error) {
	// NewStoreGate supplies an absolute path. Use the extended form so portable
	// directories beyond MAX_PATH remain usable without a registry setting.
	if !strings.HasPrefix(path, `\\?\`) {
		if strings.HasPrefix(path, `\\`) {
			path = `\\?\UNC\` + path[2:]
		} else {
			path = `\\?\` + path
		}
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, fmt.Errorf("checkpoint store lock: %w", err)
	}
	// share mode 0 refuses competing opens immediately; nil security attributes
	// make the handle non-inheritable. OPEN_ALWAYS never truncates existing data.
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE,
		0, nil, windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, fmt.Errorf("%w: %w", ErrStoreBusy, err)
		}
		return nil, fmt.Errorf("checkpoint store lock: %w", err)
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return nil, errors.Join(fmt.Errorf("checkpoint store lock: %w", err), windows.CloseHandle(handle))
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		return nil, errors.Join(errors.New("checkpoint store lock must be a regular file, not a reparse point or directory"), windows.CloseHandle(handle))
	}
	return func() error { return windows.CloseHandle(handle) }, nil
}
