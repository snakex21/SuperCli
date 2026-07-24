// Package memory is the in-process memory store. F2.b adds
// SQLite persistence and FTS5 search; this in-memory MemStore
// stays as a lightweight fallback and test fixture.
package memory

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// MemStore is an in-memory, non-concurrent memory store. Useful
// for unit tests and the `--no-memory` CLI mode. The persistent
// FileStore in store.go is what main.go wires in production.
type MemStore struct {
	entries map[string]Entry
}

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore {
	return &MemStore{entries: make(map[string]Entry)}
}

// Put adds or replaces an entry by ID. Empty ID or Content is
// rejected; Scope defaults to "general" so legacy callers that
// predate F2.b keep working.
func (s *MemStore) Put(e Entry) error {
	if e.ID == "" {
		return fmt.Errorf("memory.MemStore.Put: id is empty")
	}
	if e.Content == "" {
		return fmt.Errorf("memory.MemStore.Put(%s): content is empty", e.ID)
	}
	if e.Scope == "" {
		e.Scope = "general"
	}
	now := time.Now().UTC()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = now
	}
	e.UpdatedAt = now
	s.entries[e.ID] = e
	return nil
}

// Get returns an entry by ID.
func (s *MemStore) Get(id string) (Entry, bool) {
	e, ok := s.entries[id]
	return e, ok
}

// Delete removes an entry. Returns true if it existed.
func (s *MemStore) Delete(id string) bool {
	_, ok := s.entries[id]
	if ok {
		delete(s.entries, id)
	}
	return ok
}

// Len reports the number of stored entries.
func (s *MemStore) Len() int { return len(s.entries) }

// Search performs a case-insensitive substring match over Content
// AND any tag. Results are sorted by CreatedAt descending.
func (s *MemStore) Search(query string, limit int) []Entry {
	if query == "" {
		return nil
	}
	q := strings.ToLower(query)
	hits := make([]Entry, 0)
	for _, e := range s.entries {
		if strings.Contains(strings.ToLower(e.Content), q) {
			hits = append(hits, e)
			continue
		}
		for _, tag := range e.Tags {
			if strings.Contains(strings.ToLower(tag), q) {
				hits = append(hits, e)
				break
			}
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		return hits[i].CreatedAt.After(hits[j].CreatedAt)
	})
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}
