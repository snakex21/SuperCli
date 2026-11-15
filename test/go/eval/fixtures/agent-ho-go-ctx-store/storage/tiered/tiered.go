// Package tiered combines a fast and a slow store.
package tiered

import (
	"errors"

	"blobhub/storage"
)

// Store reads Fast first, falls back to Slow and back-fills Fast.
// Writes go to both tiers.
type Store struct {
	Fast, Slow storage.Store
}

// Get implements storage.Store.
func (s Store) Get(key string) ([]byte, error) {
	b, err := s.Fast.Get(key)
	if err == nil {
		return b, nil
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return nil, err
	}
	b, err = s.Slow.Get(key)
	if err != nil {
		return nil, err
	}
	_ = s.Fast.Put(key, b)
	return b, nil
}

// Put implements storage.Store.
func (s Store) Put(key string, data []byte) error {
	if err := s.Slow.Put(key, data); err != nil {
		return err
	}
	return s.Fast.Put(key, data)
}

// Delete implements storage.Store.
func (s Store) Delete(key string) error {
	if err := s.Fast.Delete(key); err != nil {
		return err
	}
	return s.Slow.Delete(key)
}
