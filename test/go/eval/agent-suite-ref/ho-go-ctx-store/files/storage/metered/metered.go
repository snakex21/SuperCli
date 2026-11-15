// Package metered counts store operations.
package metered

import (
	"context"
	"errors"
	"sync/atomic"

	"blobhub/storage"
)

// Store wraps Inner and counts reads, misses and writes.
type Store struct {
	Inner                 storage.Store
	Reads, Misses, Writes atomic.Int64
}

// Get implements storage.Store.
func (s *Store) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.Reads.Add(1)
	b, err := s.Inner.Get(ctx, key)
	if errors.Is(err, storage.ErrNotFound) {
		s.Misses.Add(1)
	}
	return b, err
}

// Put implements storage.Store.
func (s *Store) Put(key string, data []byte) error {
	s.Writes.Add(1)
	return s.Inner.Put(key, data)
}

// Delete implements storage.Store.
func (s *Store) Delete(key string) error {
	s.Writes.Add(1)
	return s.Inner.Delete(key)
}
