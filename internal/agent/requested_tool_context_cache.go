package agent

import (
	"strings"
	"sync"
	"sync/atomic"

	"supercli/internal/llm"
)

const (
	requestedContextCacheMaxDefs        = 32
	requestedContextCacheMaxSourceBytes = 64 << 10
	requestedContextCacheMaxRenderBytes = 128 << 10
)

// Only the last rendered dispatcher contract list is retained. Callers still
// read current registry values and apply admission/native filtering first;
// no visibility, history, timestamp or execution policy is cached here.
type requestedToolContextSnapshot struct {
	mu sync.Mutex
	// Empty is the ordinary path. Avoid locking just to clear an already
	// empty snapshot; publication/reset still serialize under mu.
	present atomic.Bool
	defs    []llm.ToolDef
	text    string
}

func (s *requestedToolContextSnapshot) reset() {
	if !s.present.Load() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearLocked()
}

func (s *requestedToolContextSnapshot) clearLocked() {
	s.defs = nil
	s.text = ""
	s.present.Store(false)
}

func requestedContextDefsCacheable(defs []llm.ToolDef) bool {
	if len(defs) == 0 || len(defs) > requestedContextCacheMaxDefs {
		return false
	}
	remaining := requestedContextCacheMaxSourceBytes
	for _, def := range defs {
		for _, value := range [...]string{def.Name, def.Description, def.Schema} {
			if len(value) > remaining {
				return false
			}
			remaining -= len(value)
		}
	}
	return true
}

func sameRequestedContextDefs(a, b []llm.ToolDef) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Description != b[i].Description || a[i].Schema != b[i].Schema {
			return false
		}
	}
	return true
}

func (s *requestedToolContextSnapshot) render(defs []llm.ToolDef) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(defs) == 0 {
		s.clearLocked()
		return ""
	}
	cacheable := requestedContextDefsCacheable(defs)
	if cacheable && sameRequestedContextDefs(s.defs, defs) {
		return s.text
	}
	text := requestedToolContextPreamble + "\n" + renderRequestedToolDefinitions(defs)
	if !cacheable || len(text) > requestedContextCacheMaxRenderBytes {
		s.clearLocked()
		return text
	}
	owned := make([]llm.ToolDef, len(defs))
	for i, def := range defs {
		// Descriptor strings are immutable, but may be tiny substrings of
		// arbitrarily large source buffers. Own only the bounded visible bytes.
		owned[i] = llm.ToolDef{
			Name:        strings.Clone(def.Name),
			Description: strings.Clone(def.Description),
			Schema:      strings.Clone(def.Schema),
		}
	}
	s.defs, s.text = owned, text
	s.present.Store(true)
	return text
}
