package tui

import (
	"strings"
	"sync"
)

// Retain only the current filter's message indices and short presentation
// previews. No raw history is copied and no per-message normalized text is kept.
// Model copies share it so value-receiver menu redraws reuse the result.
type transcriptSearchCache struct {
	mu       sync.Mutex
	revision uint64
	valid    bool
	key      transcriptSearchKey
	matches  []transcriptMatch
}

type transcriptSearchKey struct {
	query    string
	revision uint64
	first    *msg
	count    int
}

const transcriptSearchPreviewRunes = 512

func (c *chat) invalidateTranscriptSearch() {
	if c.searchCache == nil {
		return
	}
	cache := c.searchCache
	cache.mu.Lock()
	cache.revision++
	cache.valid = false
	cache.key = transcriptSearchKey{}
	cache.matches = nil
	cache.mu.Unlock()
}

// search preserves the original full-message, whitespace-normalized matching.
// Only the returned preview is shortened, after determining the match. Cursor
// movement and redraws with an unchanged filter/history pay no normalization.
// In-progress text was never searched; flushCurrent invalidates once it joins
// the completed messages. New/loaded chats start with an independent cache.
func (c *chat) search(query string) []transcriptMatch {
	q := strings.ToLower(strings.TrimSpace(query))
	if c.searchCache == nil {
		c.searchCache = new(transcriptSearchCache)
	}
	cache := c.searchCache
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if q == "" {
		cache.valid = false
		cache.key = transcriptSearchKey{}
		cache.matches = nil
		return nil
	}
	key := transcriptSearchKey{query: q, revision: cache.revision, count: len(c.msgs)}
	if len(c.msgs) > 0 {
		key.first = &c.msgs[0]
	}
	if cache.valid && cache.key == key {
		return cache.matches
	}
	pattern := newTranscriptSearchPattern(q)
	matches := make([]transcriptMatch, 0, 8)
	for i, m := range c.msgs {
		var preview string
		if pattern.useStdlib {
			plain := strings.Join(strings.Fields(m.text), " ")
			if !strings.Contains(strings.ToLower(plain), q) {
				continue
			}
			preview = transcriptSearchPreview(plain)
		} else {
			if !pattern.containsNormalized(m.text) {
				continue
			}
			preview = transcriptSearchNormalizedPreview(m.text)
		}
		matches = append(matches, transcriptMatch{MessageIndex: i, Role: m.role, Preview: preview})
	}
	cache.key, cache.matches, cache.valid = key, matches, true
	// Callers only read matches. Publishing a replacement leaves any result
	// already held by a copied model immutable.
	return matches
}

func transcriptSearchPreview(plain string) string {
	runes := 0
	for offset := range plain {
		if runes == transcriptSearchPreviewRunes {
			// Concatenating the nonempty ellipsis copies this bounded prefix;
			// it cannot retain the full normalized message's backing storage.
			return plain[:offset] + "…"
		}
		runes++
	}
	return strings.Clone(plain)
}
