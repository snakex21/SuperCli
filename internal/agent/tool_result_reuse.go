package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"supercli/internal/tools"
)

const (
	resultReuseEntries    = 16
	resultReuseBytes      = 1 << 20
	resultReuseEntryBytes = 128 << 10
	resultReuseMaxTTL     = 2 * time.Minute
)

type reusedToolResult struct {
	result  tools.Result
	created time.Time
	ttl     time.Duration
	bytes   int
}

type pendingToolResult struct{ done chan struct{} }
type resultReuseClaim struct {
	key        [sha256.Size]byte
	generation uint64
	pending    *pendingToolResult
}

// No bodies are persisted. Only tools explicitly registered as reusable and
// read-only participate. An in-flight duplicate waits on completion, not a poll;
// its call/result protocol pair is still emitted by the ordinary dispatcher.
type toolResultReuse struct {
	mu         sync.Mutex
	generation uint64
	entries    map[[sha256.Size]byte]reusedToolResult
	order      [][sha256.Size]byte
	pending    map[[sha256.Size]byte]*pendingToolResult
	bytes      int
}

func (c *toolResultReuse) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	c.entries, c.order, c.bytes = nil, nil, 0
	for _, p := range c.pending {
		close(p.done)
	}
	c.pending = nil
}

// Arguments must match an earlier successful invocation exactly. Unlike the
// repetition detector, no advisory fields are dropped and JSON numbers keep
// their precision. The caller prepares and validates arguments using the
// registry's execution rules before reaching this key, even for a cache hit.
func reusableToolKey(tool tools.Tool, args json.RawMessage) (key [sha256.Size]byte, fresh bool, ok bool) {
	if !tool.ReadOnly || tool.ReuseTTL <= 0 || tool.RefreshArg == "" || !json.Valid(args) {
		return
	}
	var values map[string]any
	d := json.NewDecoder(strings.NewReader(string(args)))
	d.UseNumber()
	if d.Decode(&values) != nil || values == nil {
		return
	}
	if value, found := values[tool.RefreshArg]; found {
		flag, valid := value.(bool)
		if !valid {
			return
		}
		fresh = flag
		delete(values, tool.RefreshArg)
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return
	}
	key = sha256.Sum256(append([]byte(tool.Name+"\x00"), encoded...))
	return key, fresh, true
}

func (c *toolResultReuse) acquire(ctx context.Context, key [sha256.Size]byte, fresh bool, now func() time.Time) (tools.Result, time.Duration, bool, resultReuseClaim, error) {
	for {
		if err := ctx.Err(); err != nil {
			return tools.Result{}, 0, false, resultReuseClaim{}, err
		}
		c.mu.Lock()
		if fresh {
			c.removeLocked(key)
			// A refresh is an independent request, never a coalesced old read.
			// Supersede any older in-flight observation of this same key so it
			// cannot later overwrite the refreshed result.
			if previous := c.pending[key]; previous != nil {
				close(previous.done)
			}
			if c.pending == nil {
				c.pending = make(map[[sha256.Size]byte]*pendingToolResult)
			}
			p := &pendingToolResult{done: make(chan struct{})}
			c.pending[key] = p
			claim := resultReuseClaim{key: key, generation: c.generation, pending: p}
			c.mu.Unlock()
			return tools.Result{}, 0, false, claim, nil
		}
		if entry, found := c.entries[key]; found {
			age := now().Sub(entry.created)
			if age >= 0 && age < entry.ttl {
				// Retain recently used evidence under cache pressure. This changes
				// eviction order only: hits never extend the observation's TTL.
				c.touchLocked(key)
				c.mu.Unlock()
				return entry.result, age, true, resultReuseClaim{}, nil
			}
			c.removeLocked(key)
		}
		if p := c.pending[key]; p != nil {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return tools.Result{}, 0, false, resultReuseClaim{}, ctx.Err()
			case <-p.done:
			}
			continue
		}
		if c.pending == nil {
			c.pending = make(map[[sha256.Size]byte]*pendingToolResult)
		}
		p := &pendingToolResult{done: make(chan struct{})}
		c.pending[key] = p
		claim := resultReuseClaim{key: key, generation: c.generation, pending: p}
		c.mu.Unlock()
		return tools.Result{}, 0, false, claim, nil
	}
}

func (c *toolResultReuse) finish(claim resultReuseClaim, result *tools.Result, ttl time.Duration, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	current := claim.pending != nil && c.pending[claim.key] == claim.pending
	if current {
		delete(c.pending, claim.key)
		close(claim.pending.done)
	}
	// Opaque effect receipts belong to checked replay, not observation TTL. Do
	// not retain their private evidence through a shallow Result value copy.
	if !current || claim.generation != c.generation || result == nil || result.Err != nil || result.Inert ||
		result.Image != nil || len(result.Images) != 0 || result.RetainedText != "" || result.CommandKey != nil || result.Operation != nil {
		return
	}
	size := len(result.Text) + len(result.ModelText) + len(result.ModelPreview)
	if size == 0 || size > resultReuseEntryBytes || ttl <= 0 {
		return
	}
	if ttl > resultReuseMaxTTL {
		ttl = resultReuseMaxTTL
	}
	c.removeLocked(claim.key)
	for len(c.order) >= resultReuseEntries || c.bytes+size > resultReuseBytes {
		c.removeLocked(c.order[0])
	}
	if c.entries == nil {
		c.entries = make(map[[sha256.Size]byte]reusedToolResult)
	}
	c.entries[claim.key] = reusedToolResult{result: *result, created: now, ttl: ttl, bytes: size}
	c.order = append(c.order, claim.key)
	c.bytes += size
}

func (c *toolResultReuse) removeLocked(key [sha256.Size]byte) {
	if entry, found := c.entries[key]; found {
		c.bytes -= entry.bytes
		delete(c.entries, key)
		for i, k := range c.order {
			if k == key {
				c.order = append(c.order[:i], c.order[i+1:]...)
				break
			}
		}
	}
}

// The bounded order is oldest-used first. Most duplicate reads already touch
// the newest entry, so that path does not scan or allocate.
func (c *toolResultReuse) touchLocked(key [sha256.Size]byte) {
	if len(c.order) == 0 || c.order[len(c.order)-1] == key {
		return
	}
	for i, old := range c.order {
		if old == key {
			copy(c.order[i:], c.order[i+1:])
			c.order[len(c.order)-1] = key
			return
		}
	}
}

func reusedResultHint(age time.Duration, refreshArg string) string {
	return fmt.Sprintf("[reuse] Successful result from %.0f seconds ago; no new request was made. Use the existing evidence for the next required action. If all requested work is complete and verified, give the final answer. Request %s=true only when a fresh observation is needed.\n", age.Seconds(), refreshArg)
}
