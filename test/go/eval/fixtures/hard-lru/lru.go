// Package lru implements a fixed-capacity least-recently-used cache.
package lru

// Cache is a string->int LRU cache. It is not safe for concurrent use.
type Cache struct {
	capacity int
	onEvict  func(key string, value int)
	items    map[string]int
	order    []string // TODO: draft bookkeeping, oldest first
}

// New creates a cache holding at most capacity entries. onEvict may be nil.
func New(capacity int, onEvict func(key string, value int)) *Cache {
	return &Cache{capacity: capacity, onEvict: onEvict, items: map[string]int{}}
}

// Get returns the value for key.
func (c *Cache) Get(key string) (int, bool) {
	v, ok := c.items[key]
	return v, ok
}

// Peek returns the value for key.
func (c *Cache) Peek(key string) (int, bool) {
	v, ok := c.items[key]
	return v, ok
}

// Put stores value under key.
func (c *Cache) Put(key string, value int) {
	c.items[key] = value
	c.order = append(c.order, key)
	if len(c.order) > c.capacity {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.items, oldest)
	}
}

// Remove deletes key and reports whether it was present.
func (c *Cache) Remove(key string) bool {
	_, ok := c.items[key]
	delete(c.items, key)
	return ok
}

// Len returns the number of stored entries.
func (c *Cache) Len() int { return len(c.items) }

// Keys returns the stored keys.
func (c *Cache) Keys() []string { return append([]string(nil), c.order...) }

// Resize changes the capacity.
func (c *Cache) Resize(capacity int) { c.capacity = capacity }
