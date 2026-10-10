//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package checkpoint

import (
	"os"
	"syscall"
)

type retentionNativeIdentity struct {
	device uint64
	inode  uint64
}

func retentionFileIdentity(path string) (retentionNativeIdentity, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return retentionNativeIdentity{}, ErrStoreInventory
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 {
		return retentionNativeIdentity{}, ErrStoreInventory
	}
	return retentionNativeIdentity{uint64(stat.Dev), uint64(stat.Ino)}, nil
}

func retentionRemoveProven(path string, proof *retentionFileProof) error {
	if err := retentionCheckProof(path, proof); err != nil {
		return err
	}
	// POSIX unlink does not require changing the object's read-only mode.
	return os.Remove(path)
}
