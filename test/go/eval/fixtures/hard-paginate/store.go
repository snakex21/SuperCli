// Package pager is an in-memory item store with cursor-based pagination.
package pager

import "sort"

// Item is a stored record. IDs are unique and may be any int.
type Item struct {
	ID   int
	Name string
}

// Store keeps items sorted ascending by ID.
type Store struct {
	items []Item
}

// NewStore creates a store containing items (later duplicates replace earlier ones).
func NewStore(items ...Item) *Store {
	s := &Store{}
	for _, it := range items {
		s.Put(it)
	}
	return s
}

// Put inserts it, or replaces the item with the same ID.
func (s *Store) Put(it Item) {
	i := sort.Search(len(s.items), func(i int) bool { return s.items[i].ID >= it.ID })
	if i < len(s.items) && s.items[i].ID == it.ID {
		s.items[i] = it
		return
	}
	s.items = append(s.items, Item{})
	copy(s.items[i+1:], s.items[i:])
	s.items[i] = it
}

// Delete removes the item with the given ID and reports whether it existed.
func (s *Store) Delete(id int) bool {
	i := sort.Search(len(s.items), func(i int) bool { return s.items[i].ID >= id })
	if i >= len(s.items) || s.items[i].ID != id {
		return false
	}
	s.items = append(s.items[:i], s.items[i+1:]...)
	return true
}

// Len returns the number of stored items.
func (s *Store) Len() int { return len(s.items) }

// firstAfter returns the index of the first item whose ID is greater than id.
func (s *Store) firstAfter(id int) int {
	return sort.Search(len(s.items), func(i int) bool { return s.items[i].ID >= id })
}
