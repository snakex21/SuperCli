package search

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// WalkFileEntriesBounded collects an optional workspace sample. Unlike a code
// search, it stops after maxEntries directory entries (including directories)
// or cancellation. It reads directories in batches, reuses their metadata and
// shares WalkFiles' exclusions. Symlinks are never followed. A false complete
// result must not be presented as an exhaustive view of the workspace.
func WalkFileEntriesBounded(ctx context.Context, root string, maxEntries int, fn func(string, fs.DirEntry) error) (complete bool, err error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if maxEntries <= 0 {
		return false, nil
	}
	info, err := os.Lstat(root)
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		if info.Mode().IsRegular() {
			return true, fn(root, fs.FileInfoToDirEntry(info))
		}
		return true, nil
	}
	remaining := maxEntries
	complete = true
	limit := errors.New("workspace sample limit")
	var walk func(string) error
	walk = func(dir string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		f, err := os.Open(dir)
		if err != nil {
			if os.IsPermission(err) || os.IsNotExist(err) {
				complete = false
				return nil
			}
			return err
		}
		defer f.Close()
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			if remaining == 0 {
				return limit
			}
			batchSize := min(128, remaining)
			entries, readErr := f.ReadDir(batchSize)
			for _, entry := range entries {
				if err := ctx.Err(); err != nil {
					return err
				}
				if remaining == 0 {
					return limit
				}
				remaining--
				path := filepath.Join(dir, entry.Name())
				if entry.IsDir() {
					if IsSkippedSubtree(root, path) {
						continue
					}
					if err := walk(path); err != nil {
						return err
					}
				} else if entry.Type().IsRegular() {
					if err := fn(path, entry); err != nil {
						return err
					}
				}
			}
			if readErr == io.EOF {
				return nil
			}
			if readErr != nil {
				return readErr
			}
		}
	}
	err = walk(root)
	if errors.Is(err, limit) {
		return false, nil
	}
	return complete && err == nil, err
}
