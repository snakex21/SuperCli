// Package lru is a fixed-capacity least-recently-used byte store.
package lru

import "container/list"

type entry struct {
	key string
	val []byte
}

// Store keeps at most Cap entries and drops the least recently used one
// when a Put would exceed it.
type Store struct {
	Cap int
	// OnEvict, when set, is called with the key of every entry that leaves
	// the store, whatever the reason.
	OnEvict func(key string)

	ll    *list.List
	items map[string]*list.Element
}

// New returns an empty store holding at most capacity entries.
func New(capacity int) *Store {
	return &Store{Cap: capacity, ll: list.New(), items: map[string]*list.Element{}}
}

// Get returns the value for key and marks it as recently used.
func (s *Store) Get(key string) ([]byte, bool) {
	e, ok := s.items[key]
	if !ok {
		return nil, false
	}
	s.ll.MoveToFront(e)
	return e.Value.(*entry).val, true
}

// Put inserts or replaces key.
func (s *Store) Put(key string, val []byte) {
	if e, ok := s.items[key]; ok {
		e.Value.(*entry).val = val
		s.ll.MoveToFront(e)
		return
	}
	s.items[key] = s.ll.PushFront(&entry{key: key, val: val})
	for s.ll.Len() > s.Cap {
		s.evictOldest()
	}
}

// Evict removes key from the store. It does nothing when key is absent.
func (s *Store) Evict(key string) {
	if e, ok := s.items[key]; ok {
		s.evict(e)
	}
}

// EvictAll empties the store, oldest entries first.
func (s *Store) EvictAll() {
	for s.ll.Len() > 0 {
		s.evictOldest()
	}
}

// Len reports the number of entries.
func (s *Store) Len() int { return s.ll.Len() }

func (s *Store) evictOldest() {
	if e := s.ll.Back(); e != nil {
		s.evict(e)
	}
}

func (s *Store) evict(e *list.Element) {
	ent := s.ll.Remove(e).(*entry)
	delete(s.items, ent.key)
	if s.OnEvict != nil {
		s.OnEvict(ent.key)
	}
}
