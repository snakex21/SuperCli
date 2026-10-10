package web

import (
	"container/list"
	"sync"
	"time"
)

const webSearchCacheEntries = 64

type searchCacheValue struct {
	results      []WebSearchResult
	engine       string
	fallbackFrom string
}

type searchCacheEntry struct {
	key       string
	value     searchCacheValue
	expiresAt time.Time
}

// searchCache is deliberately small and process-local: it avoids repeated
// network work without adding disk I/O, cleanup goroutines, or persistent
// state. Values are copied at the boundary so callers cannot mutate entries.
type searchCache struct {
	mu         sync.Mutex
	max        int
	entries    map[string]*list.Element
	lru        *list.List
	generation uint64
}

func newSearchCache(max int) *searchCache {
	if max <= 0 {
		max = webSearchCacheEntries
	}
	return &searchCache{
		max:     max,
		entries: make(map[string]*list.Element, max),
		lru:     list.New(),
	}
}

func (c *searchCache) get(key string, now time.Time) (searchCacheValue, bool) {
	if c == nil {
		return searchCacheValue{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[key]
	if !ok {
		return searchCacheValue{}, false
	}
	entry := el.Value.(*searchCacheEntry)
	if !now.Before(entry.expiresAt) {
		c.lru.Remove(el)
		delete(c.entries, key)
		return searchCacheValue{}, false
	}
	c.lru.MoveToFront(el)
	return cloneSearchCacheValue(entry.value), true
}

// currentGeneration snapshots freshness before a request. Ordinary expiry and
// eviction do not change it; an explicit refresh supersedes older requests.
func (c *searchCache) currentGeneration() uint64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generation
}

func (c *searchCache) remove(key string) uint64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	if el, ok := c.entries[key]; ok {
		c.lru.Remove(el)
		delete(c.entries, key)
	}
	return c.generation
}

func (c *searchCache) set(key string, value searchCacheValue, ttl time.Duration, now time.Time) {
	if c == nil || ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setLocked(key, value, ttl, now)
}

func (c *searchCache) setIfCurrent(key string, value searchCacheValue, ttl time.Duration, now time.Time, generation uint64) {
	if c == nil || ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if generation != c.generation {
		return
	}
	c.setLocked(key, value, ttl, now)
}

func (c *searchCache) setLocked(key string, value searchCacheValue, ttl time.Duration, now time.Time) {
	value = cloneSearchCacheValue(value)
	if el, ok := c.entries[key]; ok {
		entry := el.Value.(*searchCacheEntry)
		entry.value = value
		entry.expiresAt = now.Add(ttl)
		c.lru.MoveToFront(el)
		return
	}
	el := c.lru.PushFront(&searchCacheEntry{key: key, value: value, expiresAt: now.Add(ttl)})
	c.entries[key] = el
	for c.lru.Len() > c.max {
		oldest := c.lru.Back()
		entry := oldest.Value.(*searchCacheEntry)
		delete(c.entries, entry.key)
		c.lru.Remove(oldest)
	}
}

func cloneSearchCacheValue(v searchCacheValue) searchCacheValue {
	v.results = append([]WebSearchResult(nil), v.results...)
	return v
}
