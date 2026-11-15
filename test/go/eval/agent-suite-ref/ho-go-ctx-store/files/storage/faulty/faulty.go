// Package faulty injects read failures for resilience tests.
package faulty

import (
	"context"
	"errors"
	"sync"

	"blobhub/storage"
)

// ErrInjected is returned by every Nth Get.
var ErrInjected = errors.New("faulty: injected failure")

// Store fails every Nth Get (N <= 0 never fails).
type Store struct {
	Inner storage.Store
	N     int

	mu    sync.Mutex
	calls int
}

// Get implements storage.Store.
func (s *Store) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.calls++
	fail := s.N > 0 && s.calls%s.N == 0
	s.mu.Unlock()
	if fail {
		return nil, ErrInjected
	}
	return s.Inner.Get(ctx, key)
}

// Put implements storage.Store.
func (s *Store) Put(key string, data []byte) error { return s.Inner.Put(key, data) }

// Delete implements storage.Store.
func (s *Store) Delete(key string) error { return s.Inner.Delete(key) }
