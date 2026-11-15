// Package storage defines the blob store abstraction.
package storage

import (
	"context"
	"errors"
)

// ErrNotFound is returned by Get for unknown keys.
var ErrNotFound = errors.New("storage: not found")

// ErrReadOnly is returned by stores that reject writes.
var ErrReadOnly = errors.New("storage: read-only")

// Store is a key/value blob store.
type Store interface {
	// Get returns the blob stored under key or ErrNotFound. If ctx is
	// already done it returns ctx.Err() without doing any work.
	Get(ctx context.Context, key string) ([]byte, error)
	// Put stores data under key, replacing any previous blob.
	Put(key string, data []byte) error
	// Delete removes key; deleting a missing key is not an error.
	Delete(key string) error
}
