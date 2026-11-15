// Package session keeps logged-in sessions and their CSRF tokens.
package session

import (
	"time"

	"slab/lru"
	"slab/ttl"
)

// Manager stores session payloads in an LRU (the "cache") and short-lived
// CSRF tokens in an expiring map (the "store").
type Manager struct {
	cache *lru.Store
	store *ttl.Cache
}

// NewManager keeps up to max sessions; CSRF tokens live for csrfTTL.
func NewManager(max int, csrfTTL time.Duration) *Manager {
	return &Manager{cache: lru.New(max), store: ttl.NewCache(csrfTTL)}
}

// Login records a session and its CSRF token.
func (m *Manager) Login(id string, payload []byte, csrf string) {
	m.cache.Put(id, payload)
	m.store.Set("csrf:"+id, csrf)
}

// Session returns the payload of a live session.
func (m *Manager) Session(id string) ([]byte, bool) {
	return m.cache.Get(id)
}

// CSRF returns the CSRF token of a session.
func (m *Manager) CSRF(id string) (string, bool) {
	return m.store.Get("csrf:" + id)
}

// Logout forgets the session and its CSRF token.
func (m *Manager) Logout(id string) {
	m.cache.Evict(id)
	m.store.Evict("csrf:" + id)
}

// RotateCSRF replaces the CSRF token of a session.
func (m *Manager) RotateCSRF(id, token string) {
	m.store.Evict("csrf:" + id)
	m.store.Set("csrf:"+id, token)
}

// Reset logs everybody out.
func (m *Manager) Reset() {
	m.cache.EvictAll()
}
