// Package ttl is a map whose entries expire after a fixed time to live.
package ttl

import "time"

// Cache holds string values until they expire.
type Cache struct {
	TTL time.Duration
	Now func() time.Time

	vals map[string]item
}

type item struct {
	val     string
	expires time.Time
}

// NewCache returns an empty cache whose entries live for ttl.
func NewCache(ttl time.Duration) *Cache {
	return &Cache{TTL: ttl, Now: time.Now, vals: map[string]item{}}
}

// Set stores val under key.
func (c *Cache) Set(key, val string) {
	c.vals[key] = item{val: val, expires: c.Now().Add(c.TTL)}
}

// Get returns the value for key unless it has expired.
func (c *Cache) Get(key string) (string, bool) {
	it, ok := c.vals[key]
	if !ok || !c.Now().Before(it.expires) {
		return "", false
	}
	return it.val, true
}

// Evict drops key immediately, expired or not.
func (c *Cache) Evict(key string) {
	delete(c.vals, key)
}

// Sweep drops every expired entry and reports how many it dropped.
func (c *Cache) Sweep() int {
	n := 0
	now := c.Now()
	for k, it := range c.vals {
		if !now.Before(it.expires) {
			delete(c.vals, k)
			n++
		}
	}
	return n
}
