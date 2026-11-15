# slab API

## lru.Store

- `New(capacity int) *Store`
- `Store.Get(key string) ([]byte, bool)`
- `Store.Put(key string, val []byte)`
- `Store.Evict(key string)` - removes key; no-op when absent.
- `Store.EvictAll()` - empties the store.
- `Store.OnEvict func(key string)` - hook called for every entry that leaves the store.

## ttl.Cache

- `NewCache(ttl time.Duration) *Cache`
- `Cache.Set(key, val string)` / `Cache.Get(key string) (string, bool)`
- `Cache.Evict(key string)` - drops key immediately.
- `Cache.Sweep() int` - drops expired entries.

## admin

- `PurgeKeys(s *lru.Store, keys []string) int` - returns how many keys it was asked to purge.
- `PurgeTokens(c *ttl.Cache, tokens []string)`
- `Flush(s *lru.Store) int`
