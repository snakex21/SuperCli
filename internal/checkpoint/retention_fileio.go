package checkpoint

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func retentionFileSize(path string) (int64, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, ErrStoreInventory
	}
	return info.Size(), nil
}

// Historical paths must not escape through a later symlink/junction. Check all
// existing ancestors, including staging/promote/rollback destinations.
func retentionSafePath(root, target string) error {
	if !filepath.IsAbs(root) || !within(root, target) {
		return ErrStoreInventory
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	current := root
	for _, part := range append([]string{""}, strings.Split(rel, string(filepath.Separator))...) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.Mode().IsRegular() && !info.IsDir()) {
			return ErrStoreInventory
		}
	}
	return nil
}

func retentionAtomicWrite(root, target string, data []byte) (err error) {
	if err := retentionSafePath(root, target); err != nil {
		return err
	}
	// Unique exclusively created staging avoids overwriting a prior interrupted
	// staging file. Leftovers are counted/protected by census, never deleted by
	// name or guessed owner age. All staging remains beside its portable target.
	f, err := os.CreateTemp(filepath.Dir(target), filepath.Base(target)+".quota-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	closed := false
	defer func() {
		if !closed {
			err = errors.Join(err, f.Close())
		}
		removeErr := os.Remove(tmp)
		if !os.IsNotExist(removeErr) {
			err = errors.Join(err, removeErr)
		}
	}()
	if err := retentionSafePath(root, tmp); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	if err = retentionSafePath(root, target); err != nil {
		return err
	}
	return retentionPromote(tmp, target)
}
