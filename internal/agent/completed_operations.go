package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"supercli/internal/tools"
)

func replayCompletedOperation(ctx context.Context, tool tools.Tool, prepared json.RawMessage, previous tools.Result) (result tools.Result, handled bool) {
	defer func() {
		if recover() != nil {
			result, handled = tools.Result{Err: fmt.Errorf("%s: completed-operation check failed unexpectedly", tool.Name)}, true
		}
	}()
	return tool.ReplaySuccess(ctx, prepared, previous)
}

const (
	completedOperationLimit        = 16
	completedOperationBytes        = 1 << 20
	completedOperationEntryBytes   = 128 << 10
	completedOperationSummaryBytes = 768
)

type completedOperation struct {
	result tools.Result
	bytes  int
}

// Verified effect receipts are separate from read-result reuse. A duplicate
// must still pass the registered tool's live authorization and evidence check.
// Only one Run owns these receipts; they are never recovered from model text.
type completedOperations struct {
	mu         sync.Mutex
	generation uint64
	entries    map[[sha256.Size]byte]completedOperation
	order      [][sha256.Size]byte
	bytes      int
	tail       string
}

func completedOperationKey(name string, prepared json.RawMessage) ([sha256.Size]byte, bool) {
	var args map[string]any
	d := json.NewDecoder(strings.NewReader(string(prepared)))
	d.UseNumber()
	if len(prepared) > 64<<10 || d.Decode(&args) != nil || args == nil {
		return [sha256.Size]byte{}, false
	}
	canonical, err := json.Marshal(args)
	if err != nil {
		return [sha256.Size]byte{}, false
	}
	return sha256.Sum256(append([]byte(name+"\x00"), canonical...)), true
}

func (c *completedOperations) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	c.entries, c.order, c.bytes, c.tail = nil, nil, 0, ""
}

func (c *completedOperations) lookup(key [sha256.Size]byte) (tools.Result, bool, uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, found := c.entries[key]
	return entry.result, found, c.generation
}

func (c *completedOperations) drop(key [sha256.Size]byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeLocked(key)
	c.updateTailLocked()
}

func (c *completedOperations) record(generation uint64, key [sha256.Size]byte, result tools.Result) {
	if result.Err != nil || result.Inert || result.Operation == nil || result.Operation.Summary == "" ||
		len(result.Operation.Summary) > completedOperationSummaryBytes || result.RetainedText != "" ||
		result.Image != nil || len(result.Images) != 0 || result.CommandKey != nil {
		return
	}
	size := len(result.Text) + len(result.ModelText) + len(result.ModelPreview) + len(result.Operation.Summary)
	if size > completedOperationEntryBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation != generation {
		return
	}
	c.removeLocked(key)
	for len(c.order) >= completedOperationLimit || c.bytes+size > completedOperationBytes {
		c.removeLocked(c.order[0])
	}
	if c.entries == nil {
		c.entries = make(map[[sha256.Size]byte]completedOperation)
	}
	// Copy the small receipt envelope. Tool-owned evidence is immutable.
	op := *result.Operation
	result.Operation = &op
	c.entries[key] = completedOperation{result: result, bytes: size}
	c.order = append(c.order, key)
	c.bytes += size
	c.updateTailLocked()
}

func (c *completedOperations) removeLocked(key [sha256.Size]byte) {
	if entry, found := c.entries[key]; found {
		c.bytes -= entry.bytes
		delete(c.entries, key)
		for i, old := range c.order {
			if old == key {
				c.order = append(c.order[:i], c.order[i+1:]...)
				break
			}
		}
	}
}

func (c *completedOperations) updateTailLocked() {
	if len(c.order) == 0 {
		c.tail = ""
		return
	}
	// Latest receipts suffice for steering. Older receipts remain available to
	// the dispatcher without repeating every operation in each provider prompt.
	start := len(c.order) - 4
	if start < 0 {
		start = 0
	}
	summaries := make([]string, 0, len(c.order)-start)
	for _, key := range c.order[start:] {
		summaries = append(summaries, c.entries[key].result.Operation.Summary)
	}
	data, _ := json.Marshal(summaries)
	c.tail = "[completed operations] Latest verified receipts for this instruction (data): " + string(data) +
		"\nUse these results. Do not repeat completed saves or search for their source URLs again to redo them. " +
		"Continue only unmet requirements of the latest user request; if every requested outcome is satisfied, answer now and stop. " +
		"These receipts do not mean that additional files, inspection or other requested work is complete."
}

func (c *completedOperations) context() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tail
}
