// Package fileops provides targeted file operations (F24) and
// change tracking for /diff (F26.4). Changes are recorded in a
// ring buffer so /diff can display unified diffs of all file
// modifications in the current session.
package fileops

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// Change records a single file modification.
type Change struct {
	Path      string
	Op        string    // "write", "edit", "insert", "delete"
	Old       string    // content before (empty for new files)
	New       string    // content after (empty for delete)
	Timestamp time.Time
}

// Tracker records file changes for /diff display.
// Safe for concurrent use.
type Tracker struct {
	mu      sync.Mutex
	changes []Change
	max     int
}

// NewTracker creates a change tracker with the given ring buffer size.
func NewTracker(max int) *Tracker {
	if max <= 0 {
		max = 100
	}
	return &Tracker{max: max}
}

// Record adds a file change to the tracker.
func (t *Tracker) Record(path, op, old, new string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	c := Change{
		Path:      path,
		Op:        op,
		Old:       old,
		New:       new,
		Timestamp: time.Now(),
	}
	if len(t.changes) >= t.max {
		// Drop oldest.
		t.changes = t.changes[1:]
	}
	t.changes = append(t.changes, c)
}

// Changes returns a copy of all recorded changes.
func (t *Tracker) Changes() []Change {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Change, len(t.changes))
	copy(out, t.changes)
	return out
}

// Clear resets the tracker.
func (t *Tracker) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.changes = nil
}

// Count returns the number of recorded changes.
func (t *Tracker) Count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.changes)
}

// DiffOutput generates a unified diff for /diff display.
// Returns empty string if no changes recorded.
func (t *Tracker) DiffOutput() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.changes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("_[%d file change(s) in session]_\n\n", len(t.changes)))
	for i, c := range t.changes {
		header := fmt.Sprintf("--- %s (%s)", c.Path, c.Op)
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(header + "\n")
		// Show first 20 lines of each side.
		oldLines := strings.Split(truncateLines(c.Old, 20), "\n")
		newLines := strings.Split(truncateLines(c.New, 20), "\n")
		for _, l := range oldLines {
			b.WriteString("- " + l + "\n")
		}
		for _, l := range newLines {
			b.WriteString("+ " + l + "\n")
		}
	}
	return b.String()
}

// truncateLines returns at most n lines from text.
func truncateLines(text string, n int) string {
	lines := strings.Split(text, "\n")
	if len(lines) > n {
		lines = lines[:n]
		lines = append(lines, fmt.Sprintf("... (%d more lines)", len(lines)))
	}
	return strings.Join(lines, "\n")
}

// UndoResult describes a single reverted change.
type UndoResult struct {
	Path string
	Op   string
}

// Undo reverts the last n changes by writing the Old content back
// to each file. Returns the list of reverted changes.
func (t *Tracker) Undo(n int) ([]UndoResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if n <= 0 {
		n = 1
	}
	if n > len(t.changes) {
		n = len(t.changes)
	}
	if n == 0 {
		return nil, fmt.Errorf("no changes to undo")
	}

	// Pop last n changes (reversed order).
	undone := make([]UndoResult, 0, n)
	for i := 0; i < n; i++ {
		idx := len(t.changes) - 1 - i
		c := t.changes[idx]
		// Write Old content back to the file.
		if err := writeBytes(c.Path, []byte(c.Old)); err != nil {
			// Return what we've undone so far.
			return undone, fmt.Errorf("undo %s: %w", c.Path, err)
		}
		undone = append(undone, UndoResult{Path: c.Path, Op: c.Op})
	}

	// Trim the tracker.
	t.changes = t.changes[:len(t.changes)-n]
	return undone, nil
}

// writeBytes writes data to a file, creating it if needed.
func writeBytes(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}
