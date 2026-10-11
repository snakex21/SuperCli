package core

import (
	"math/big"
	"strconv"
	"strings"
	"sync"
)

const (
	schemaCacheMaxEntries     = 128
	schemaCacheMaxBytes       = 4 << 20
	schemaCacheMaxSourceBytes = 8 << 10
	schemaCacheMaxPlanBytes   = 128 << 10
	schemaCacheMaxNodes       = 256
	schemaCacheMaxDepth       = 64
	schemaCacheMaxTextBytes   = 32 << 10
	schemaCacheMaxNumberBits  = 512
)

// registeredSchemaCache holds only private, immutable validation plans. Tool
// callbacks, visibility, roots, outputs and permissions remain registry-local.
// Entries use exact source bytes; successful compilation is the sole producer.
var registeredSchemaCache = newCompiledSchemaCache(schemaCacheMaxEntries, schemaCacheMaxBytes)

type compiledSchemaCacheEntry struct {
	plan   *compiledToolSchema
	weight int
}

type compiledSchemaCache struct {
	mu       sync.RWMutex
	entries  map[string]compiledSchemaCacheEntry
	order    []string
	head     int
	bytes    int
	maxBytes int
}

func newCompiledSchemaCache(maxEntries, maxBytes int) *compiledSchemaCache {
	if maxEntries < 0 {
		maxEntries = 0
	}
	return &compiledSchemaCache{
		entries:  make(map[string]compiledSchemaCacheEntry),
		order:    make([]string, maxEntries),
		maxBytes: maxBytes,
	}
}

func compileRegisteredToolSchema(raw string) (*compiledToolSchema, error) {
	return registeredSchemaCache.compile(raw)
}

func (c *compiledSchemaCache) compile(raw string) (*compiledToolSchema, error) {
	if len(raw) > schemaCacheMaxSourceBytes || len(c.order) == 0 || c.maxBytes <= 0 {
		return compileToolSchema(raw)
	}
	c.mu.RLock()
	entry, ok := c.entries[raw]
	c.mu.RUnlock()
	if ok {
		return entry.plan, nil
	}

	// Compilation and eligibility traversal never hold the global cache lock.
	// Cold concurrent misses may compile independently; insertion picks one
	// immutable plan without serializing unrelated schemas or callbacks.
	plan, err := compileToolSchema(raw)
	if err != nil || plan == nil {
		return plan, err
	}
	weight, eligible := compiledSchemaCacheWeight(raw, plan)
	if !eligible || weight > c.maxBytes {
		return plan, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[raw]; ok {
		return entry.plan, nil
	}
	for len(c.entries) >= len(c.order) || c.bytes > c.maxBytes-weight {
		key := c.order[c.head]
		c.bytes -= c.entries[key].weight
		delete(c.entries, key)
		c.order[c.head] = ""
		c.head = (c.head + 1) % len(c.order)
	}
	// A short source may be a substring of a much larger caller allocation.
	// Both map and FIFO refer to this owned copy, never the caller's backing data.
	key := strings.Clone(raw)
	c.order[(c.head+len(c.entries))%len(c.order)] = key
	c.entries[key] = compiledSchemaCacheEntry{plan: plan, weight: weight}
	c.bytes += weight
	return plan, nil
}

// compiledSchemaCacheWeight is conservative residency accounting, not a Go
// heap/RSS measurement. Its source-scaled reserve covers compiler map hints
// from duplicate type/enum arrays: map len cannot expose retained buckets.
// Tree, maps, slice capacity, retained text and big-number backing words are
// charged separately. Repeated string references are counted again rather
// than relying on compiler/decoder sharing. Regex programs and unusually
// expanded plans take the unchanged, uncached compiler path.
func compiledSchemaCacheWeight(raw string, plan *compiledToolSchema) (int, bool) {
	if plan == nil || plan.root == nil || len(raw) > schemaCacheMaxSourceBytes {
		return 0, false
	}
	w := schemaCacheWeight{bytes: 320 + 32*len(raw)}
	if !w.add(cap(plan.coercions) * 64) {
		return 0, false
	}
	for _, field := range plan.coercions {
		if !w.text(field.name) || !w.text(field.want) {
			return 0, false
		}
	}
	if !w.node(plan.root, 1) {
		return 0, false
	}
	return w.bytes, true
}

type schemaCacheWeight struct {
	bytes     int
	textBytes int
	nodes     int
}

func (w *schemaCacheWeight) add(bytes int) bool {
	if bytes < 0 || bytes > schemaCacheMaxPlanBytes-w.bytes {
		return false
	}
	w.bytes += bytes
	return true
}

func (w *schemaCacheWeight) text(value string) bool {
	if len(value) > schemaCacheMaxSourceBytes || len(value) > schemaCacheMaxTextBytes-w.textBytes {
		return false
	}
	w.textBytes += len(value)
	return w.add(len(value))
}

func (w *schemaCacheWeight) strings(values []string) bool {
	if !w.add(32 + cap(values)*32) {
		return false
	}
	for _, value := range values {
		if !w.text(value) {
			return false
		}
	}
	return true
}

func (w *schemaCacheWeight) number(value *big.Rat) bool {
	if value == nil {
		return true
	}
	const maxWords = schemaCacheMaxNumberBits/strconv.IntSize + 8
	numerator, denominator := value.Num(), value.Denom()
	if numerator.BitLen() > schemaCacheMaxNumberBits || denominator.BitLen() > schemaCacheMaxNumberBits ||
		cap(numerator.Bits()) > maxWords || cap(denominator.Bits()) > maxWords {
		return false
	}
	return w.add(128 + (cap(numerator.Bits())+cap(denominator.Bits()))*(strconv.IntSize/8))
}

func (w *schemaCacheWeight) node(n *schemaNode, depth int) bool {
	if n == nil {
		return true
	}
	w.nodes++
	if w.nodes > schemaCacheMaxNodes || depth > schemaCacheMaxDepth || n.pattern != nil || !w.add(512) {
		return false
	}
	if !w.add(128 + len(n.types)*128) {
		return false
	}
	for name := range n.types {
		if !w.text(name) {
			return false
		}
	}
	if !w.add(128 + len(n.properties)*128) {
		return false
	}
	for name, child := range n.properties {
		if !w.text(name) || !w.node(child, depth+1) {
			return false
		}
	}
	if !w.strings(n.propertyOrder) || !w.strings(n.required) ||
		!w.node(n.additional, depth+1) || !w.node(n.items, depth+1) ||
		!w.number(n.minimum) || !w.number(n.maximum) {
		return false
	}
	if !w.add(128+len(n.enumKeys)*128) || !w.text(n.enumHint) || !w.text(n.constKey) {
		return false
	}
	for key := range n.enumKeys {
		if !w.text(key) {
			return false
		}
	}
	for _, group := range [][]*schemaNode{n.prefix, n.anyOf, n.oneOf, n.allOf} {
		if !w.add(32 + cap(group)*16) {
			return false
		}
		for _, child := range group {
			if !w.node(child, depth+1) {
				return false
			}
		}
	}
	return true
}
