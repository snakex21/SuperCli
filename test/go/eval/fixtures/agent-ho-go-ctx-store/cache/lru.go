// Package cache is a small in-process LRU for rendered thumbnails.
// It is not a storage.Store: lookups cannot fail, they only miss.
package cache

import "container/list"

// Cache is the thumbnail cache contract.
type Cache interface {
	Get(key string) ([]byte, bool)
	Add(key string, data []byte)
}

type entry struct {
	key  string
	data []byte
}

// LRU is a fixed-size least-recently-used Cache.
type LRU struct {
	max   int
	order *list.List
	items map[string]*list.Element
}

// NewLRU returns an LRU holding at most max entries.
func NewLRU(max int) *LRU {
	return &LRU{max: max, order: list.New(), items: map[string]*list.Element{}}
}

// Get returns the cached blob and marks it recently used.
func (c *LRU) Get(key string) ([]byte, bool) {
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*entry).data, true
}

// Add inserts or refreshes key, evicting the oldest entry when full.
func (c *LRU) Add(key string, data []byte) {
	if el, ok := c.items[key]; ok {
		el.Value.(*entry).data = data
		c.order.MoveToFront(el)
		return
	}
	c.items[key] = c.order.PushFront(&entry{key, data})
	if c.order.Len() > c.max {
		old := c.order.Back()
		c.order.Remove(old)
		delete(c.items, old.Value.(*entry).key)
	}
}
