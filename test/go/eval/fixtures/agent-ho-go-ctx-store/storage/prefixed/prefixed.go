// Package prefixed namespaces keys of another store.
package prefixed

import "blobhub/storage"

// Store prepends Prefix to every key before calling Inner.
type Store struct {
	Inner  storage.Store
	Prefix string
}

// Get implements storage.Store.
func (s Store) Get(key string) ([]byte, error) { return s.Inner.Get(s.Prefix + key) }

// Put implements storage.Store.
func (s Store) Put(key string, data []byte) error { return s.Inner.Put(s.Prefix+key, data) }

// Delete implements storage.Store.
func (s Store) Delete(key string) error { return s.Inner.Delete(s.Prefix + key) }
