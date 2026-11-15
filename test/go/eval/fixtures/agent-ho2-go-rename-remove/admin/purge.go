// Package admin holds maintenance operations used by slabctl.
package admin

import (
	"slab/lru"
	"slab/ttl"
)

// PurgeKeys removes keys from an LRU store and reports how many keys it
// was asked to purge.
func PurgeKeys(s *lru.Store, keys []string) int {
	for _, k := range keys {
		s.Evict(k)
	}
	return len(keys)
}

// PurgeTokens drops tokens from an expiring cache.
func PurgeTokens(c *ttl.Cache, tokens []string) {
	for _, tok := range tokens {
		c.Evict(tok)
	}
}

// Flush empties an LRU store and returns how many entries it held.
func Flush(s *lru.Store) int {
	n := s.Len()
	s.EvictAll()
	return n
}
