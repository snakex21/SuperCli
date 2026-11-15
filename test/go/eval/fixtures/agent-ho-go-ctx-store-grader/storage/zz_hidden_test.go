package storage_test

import (
	"context"

	"blobhub/cache"
	"blobhub/storage"
	"blobhub/storage/disk"
	"blobhub/storage/faulty"
	"blobhub/storage/memory"
	"blobhub/storage/metered"
	"blobhub/storage/prefixed"
	"blobhub/storage/readonly"
	"blobhub/storage/tiered"
)

type wantStore interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Put(key string, data []byte) error
	Delete(key string) error
}

var (
	_ wantStore     = storage.Store(nil)
	_ storage.Store = wantStore(nil)
	_ storage.Store = (*memory.Store)(nil)
	_ storage.Store = disk.Store{}
	_ storage.Store = prefixed.Store{}
	_ storage.Store = tiered.Store{}
	_ storage.Store = (*metered.Store)(nil)
	_ storage.Store = readonly.Store{}
	_ storage.Store = (*faulty.Store)(nil)
	_ cache.Cache   = (*cache.LRU)(nil)
)

type oldCache interface {
	Get(key string) ([]byte, bool)
	Add(key string, data []byte)
}

var _ oldCache = cache.Cache(nil)
