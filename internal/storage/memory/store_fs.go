package memory

import (
	"fmt"
	"os"
	"path/filepath"
)

// mkdirAll is a thin wrapper that returns a memory-package
// formatted error. We avoid importing storage here to keep
// this package self-contained for tests.
func mkdirAll(path string, perm os.FileMode) error {
	if err := os.MkdirAll(path, perm); err != nil {
		return fmt.Errorf("mkdir %q: %w", path, err)
	}
	return nil
}

// readDir returns the names of immediate children of dir.
func readDir(dir string) ([]string, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	names, err := f.Readdirnames(0)
	if err != nil {
		return nil, err
	}
	return names, nil
}

// renameFile renames src to dst. Used by ArchiveOldScratches.
func renameFile(src, dst string) error {
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("rename %q -> %q: %w", src, dst, err)
	}
	return nil
}

// tempHome returns a fresh temporary directory and ensures
// it is cleaned up on program exit. It is used by OpenStore("")
// to support tests and the --no-memory flag.
func tempHome() (string, error) {
	dir, err := os.MkdirTemp("", "supercli-mem-*")
	if err != nil {
		return "", fmt.Errorf("memory.tempHome: %w", err)
	}
	// MkdirTemp creates the leaf; we need the memory/ structure
	// underneath too.
	for _, sub := range []string{"memory", filepath.Join("memory", "patterns"), filepath.Join("memory", "archive")} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return "", err
		}
	}
	return dir, nil
}
