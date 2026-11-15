// Package memory is an in-memory storage.Store.
package memory

import (
	"context"
	"sync"

	"blobhub/storage"
)

// Store keeps blobs in a map.
type Store struct {
	mu    sync.RWMutex
	blobs map[string][]byte
}

// New returns an empty store.
func New() *Store { return &Store{blobs: map[string][]byte{}} }

// Get implements storage.Store.
func (s *Store) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.blobs[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return append([]byte(nil), b...), nil
}

// Put implements storage.Store.
func (s *Store) Put(key string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blobs[key] = append([]byte(nil), data...)
	return nil
}

// Delete implements storage.Store.
func (s *Store) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.blobs, key)
	return nil
}
