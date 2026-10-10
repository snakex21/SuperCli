//go:build windows

package checkpoint

import (
	"errors"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type retentionNativeIdentity struct {
	volume uint32
	high   uint32
	low    uint32
}

func retentionWindowsPath(path string) (*uint16, error) {
	if !strings.HasPrefix(path, `\\?\`) {
		if strings.HasPrefix(path, `\\`) {
			path = `\\?\UNC\` + path[2:]
		} else {
			path = `\\?\` + path
		}
	}
	return windows.UTF16PtrFromString(path)
}

func retentionWindowsIdentity(info windows.ByHandleFileInformation) (retentionNativeIdentity, error) {
	if info.NumberOfLinks != 1 || info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 {
		return retentionNativeIdentity{}, ErrStoreInventory
	}
	return retentionNativeIdentity{info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow}, nil
}

func retentionFileIdentity(path string) (retentionNativeIdentity, error) {
	name, err := retentionWindowsPath(path)
	if err != nil {
		return retentionNativeIdentity{}, err
	}
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return retentionNativeIdentity{}, err
	}
	var info windows.ByHandleFileInformation
	err = windows.GetFileInformationByHandle(handle, &info)
	err = errors.Join(err, windows.CloseHandle(handle))
	if err != nil {
		return retentionNativeIdentity{}, err
	}
	return retentionWindowsIdentity(info)
}

// FILE_BASIC_INFO has four LARGE_INTEGER times followed by a DWORD and native
// padding. Zero times preserve timestamps. Explicit padding also covers 386.
type retentionWindowsBasicInfo struct {
	times      [4]int64
	attributes uint32
	padding    uint32
}

func retentionWindowsAttributes(handle windows.Handle, attributes uint32) error {
	if attributes == 0 {
		attributes = windows.FILE_ATTRIBUTE_NORMAL // Zero means "do not change".
	}
	basic := retentionWindowsBasicInfo{attributes: attributes}
	return windows.SetFileInformationByHandle(handle, windows.FileBasicInfo, (*byte)(unsafe.Pointer(&basic)), uint32(unsafe.Sizeof(basic)))
}

func retentionWindowsDispose(handle windows.Handle) error {
	// FILE_DISPOSITION_INFO.DeleteFile is BOOLEAN, not a 32-bit BOOL.
	disposition := byte(1)
	return windows.SetFileInformationByHandle(handle, windows.FileDispositionInfo, &disposition, 1)
}

func retentionRemoveProven(path string, proof *retentionFileProof) error {
	return retentionRemoveWindows(path, proof, retentionWindowsDispose)
}

func retentionRemoveWindows(path string, proof *retentionFileProof, dispose func(windows.Handle) error) (err error) {
	if err := retentionCheckProof(path, proof); err != nil {
		return err
	}
	name, err := retentionWindowsPath(path)
	if err != nil {
		return err
	}
	// No share-write or share-delete: the verified handle pins the exact file
	// throughout the attribute change and delete; no path-based chmod/delete
	// can accidentally operate on a concurrently substituted object.
	handle, err := windows.CreateFile(name, windows.DELETE|windows.FILE_READ_ATTRIBUTES|windows.FILE_WRITE_ATTRIBUTES, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			err = errors.Join(err, windows.CloseHandle(handle))
		}
	}()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	identity, err := retentionWindowsIdentity(info)
	size := int64(uint64(info.FileSizeHigh)<<32 | uint64(info.FileSizeLow))
	if err != nil || identity != proof.native || size != proof.info.Size() || info.LastWriteTime.Nanoseconds() != proof.info.ModTime().UnixNano() {
		return ErrStoreInventory
	}
	readonly := info.FileAttributes&windows.FILE_ATTRIBUTE_READONLY != 0
	if readonly {
		if err := retentionWindowsAttributes(handle, info.FileAttributes&^windows.FILE_ATTRIBUTE_READONLY); err != nil {
			return err
		}
	}
	if err := dispose(handle); err != nil {
		// Restore only the same verified identity. os.Remove's automatic Windows
		// readonly retry lacks this rollback when deletion subsequently fails.
		if readonly {
			var current windows.ByHandleFileInformation
			checkErr := windows.GetFileInformationByHandle(handle, &current)
			currentID := retentionNativeIdentity{current.VolumeSerialNumber, current.FileIndexHigh, current.FileIndexLow}
			if checkErr == nil && currentID == identity {
				checkErr = retentionWindowsAttributes(handle, info.FileAttributes)
			} else if checkErr == nil {
				checkErr = ErrStoreInventory
			}
			err = errors.Join(err, checkErr)
		}
		return err
	}
	closed = true
	if err := windows.CloseHandle(handle); err != nil {
		return err
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		// A prior reader can delay the final delete. Do not claim that these
		// physical bytes have been reclaimed until the path is gone.
		if err != nil {
			return err
		}
		return ErrStoreInventory
	}
	return nil
}
