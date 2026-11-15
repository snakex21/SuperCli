// Package disk stores blobs as files in a directory.
package disk

import (
	"context"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"blobhub/storage"
)

// Store writes one file per key (hex-encoded name) under Dir.
type Store struct {
	Dir string
}

func (s Store) path(key string) string {
	return filepath.Join(s.Dir, hex.EncodeToString([]byte(key)))
}

// Get implements storage.Store.
func (s Store) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(s.path(key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, storage.ErrNotFound
	}
	return b, err
}

// Put implements storage.Store.
func (s Store) Put(key string, data []byte) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.path(key), data, 0o644)
}

// Delete implements storage.Store.
func (s Store) Delete(key string) error {
	err := os.Remove(s.path(key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
