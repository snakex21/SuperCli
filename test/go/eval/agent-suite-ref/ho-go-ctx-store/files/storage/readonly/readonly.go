// Package readonly exposes a store without write access.
package readonly

import (
	"context"

	"blobhub/storage"
)

// Store forwards reads to Inner and rejects writes.
type Store struct {
	Inner storage.Store
}

// Get implements storage.Store.
func (s Store) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.Inner.Get(ctx, key)
}

// Put implements storage.Store.
func (Store) Put(string, []byte) error { return storage.ErrReadOnly }

// Delete implements storage.Store.
func (Store) Delete(string) error { return storage.ErrReadOnly }
