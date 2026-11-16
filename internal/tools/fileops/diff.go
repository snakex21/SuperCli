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
	Op        string // "write", "edit", "insert", "delete"
	Old       string // content before (empty for new files)
	New       string // content after (empty for delete)
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

// DiffText supplies the user-visible chrome of DiffOutputText so callers
// can localize it. Nil fields fall back to the English defaults.
type DiffText struct {
	// Summary is the session header, e.g. "2 file change(s) in session".
	// The total line stats (" [+4 -3]") are appended after it.
	Summary func(changes int) string
	// MoreLines marks a side cut after diffPreviewLines lines, e.g.
	// "... (4980 more lines)"; hidden is the exact number not shown.
	MoreLines func(hidden int) string
}

// diffPreviewLines is how many lines of each side /diff shows per change.
const diffPreviewLines = 20

func (d DiffText) summary(changes int) string {
	if d.Summary != nil {
		return d.Summary(changes)
	}
	return fmt.Sprintf("%d file change(s) in session", changes)
}

func (d DiffText) moreLines(hidden int) string {
	if d.MoreLines != nil {
		return d.MoreLines(hidden)
	}
	return fmt.Sprintf("... (%d more lines)", hidden)
}

// DiffOutput generates a unified diff for /diff display with English
// chrome. Returns empty string if no changes recorded.
func (t *Tracker) DiffOutput() string {
	return t.DiffOutputText(DiffText{})
}

// DiffOutputText is DiffOutput with caller-supplied (localized) chrome.
// The result is plain text (no Markdown): a summary line with the total
// line stats, a blank line, then one block per change.
func (t *Tracker) DiffOutputText(text DiffText) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.changes) == 0 {
		return ""
	}
	var totalAdd, totalDel int
	var b strings.Builder
	for i, c := range t.changes {
		added, removed := countLines(c.New), countLines(c.Old)
		totalAdd += added
		totalDel += removed
		header := fmt.Sprintf("--- %s (%s)%s", c.Path, c.Op, lineStats(added, removed))
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(header + "\n")
		// Show the first diffPreviewLines lines of each side.
		oldLines := strings.Split(truncateLines(c.Old, diffPreviewLines, text.moreLines), "\n")
		newLines := strings.Split(truncateLines(c.New, diffPreviewLines, text.moreLines), "\n")
		for _, l := range oldLines {
			b.WriteString("- " + l + "\n")
		}
		for _, l := range newLines {
			b.WriteString("+ " + l + "\n")
		}
	}
	return text.summary(len(t.changes)) + lineStats(totalAdd, totalDel) + "\n\n" + b.String()
}

// lineStats formats an added/removed line counter, omitting zero sides:
// "[+12 -3]", "[+5]", "[-2]", or "" when nothing changed.
func lineStats(added, removed int) string {
	switch {
	case added == 0 && removed == 0:
		return ""
	case added == 0:
		return fmt.Sprintf(" [-%d]", removed)
	case removed == 0:
		return fmt.Sprintf(" [+%d]", added)
	default:
		return fmt.Sprintf(" [+%d -%d]", added, removed)
	}
}

// countLines returns the number of lines in text; empty text has 0.
func countLines(text string) int {
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}

// truncateLines returns at most n lines from text, followed by a marker
// (built by more) that carries the exact number of hidden lines.
func truncateLines(text string, n int, more func(hidden int) string) string {
	lines := strings.Split(text, "\n")
	if len(lines) > n {
		hidden := len(lines) - n
		lines = append(lines[:n:n], more(hidden))
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
