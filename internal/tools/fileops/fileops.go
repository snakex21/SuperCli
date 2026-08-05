// Package fileops implements F24: targeted file operations.
// Instead of loading an entire file to change one line, the
// model uses these functions to read/edit specific line
// ranges — saving tokens on large files.
//
// All line numbers are 1-based (matching editor convention).
// The package is pure — no sandbox, no LLM, just file I/O.
// Each mutation function returns a human-readable diff
// (±3 context lines) for verification.
package fileops

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

const (
	// MaxLineRange caps read_lines to prevent abuse.
	MaxLineRange = 500

	// DefaultContextRadius is the default ±N lines for
	// read_context.
	DefaultContextRadius = 10

	// MaxContextRadius caps read_context so a huge radius
	// can't dump an entire large file into the context.
	MaxContextRadius = 250

	// DiffContext is the number of context lines shown
	// before/after a mutation in the diff output.
	DiffContext = 3
)

// FileErr maps a Go filesystem error to a short deterministic
// message the model can act on without parsing OS prose:
//
//	not_found <path>
//	permission <path>
//	is_directory <path>
//
// Only facts the CLI is sure of — unknown causes pass through
// unchanged. Raw Go errors like "open C:\...: The system cannot
// find the file specified." are longer and harder for small
// models to react to than a stable keyword + the exact path.
func FileErr(err error, path string) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("not_found %s", path)
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("permission %s", path)
	}
	// A read/open that failed on an existing directory: state
	// the shape fact instead of the OS-specific error text.
	if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
		return fmt.Errorf("is_directory %s", path)
	}
	return err
}

// readLines reads all lines from path. Returns the lines
// slice (0-indexed) and nil error on success. Lines do
// NOT include trailing newlines. Errors are pre-classified
// via FileErr so callers can return them verbatim.
func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, FileErr(err, path)
	}
	// Handle empty file.
	if len(data) == 0 {
		return []string{}, nil
	}
	// A binary file must never reach the line splitter: reading it
	// mangles the bytes through JSON (U+FFFD) and editing it rewrites a
	// compressed stream line by line. Refuse with a message that names
	// the tool that actually works.
	if err := ensureTextBytes(path, data, false); err != nil {
		return nil, err
	}
	raw := string(data)
	// Split into lines, stripping the trailing newline
	// from the last line if present.
	lines := strings.Split(raw, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, nil
}

// writeLines writes lines back to path with trailing newlines.
// Adds a final newline if the file originally had one (we
// always add one — POSIX convention).
func writeLines(path string, lines []string) error {
	if len(lines) == 0 {
		return os.WriteFile(path, []byte{}, 0644)
	}
	var b strings.Builder
	for i, l := range lines {
		b.WriteString(l)
		if i < len(lines)-1 {
			b.WriteByte('\n')
		}
	}
	// Add trailing newline (POSIX).
	b.WriteByte('\n')
	return os.WriteFile(path, []byte(b.String()), 0644)
}

// LineRange represents a single line with its 1-based number.
type LineRange struct {
	Number  int    `json:"number"`
	Content string `json:"content"`
}

// ReadLines reads lines from..to (inclusive, 1-based) from
// path. Returns the lines with their numbers. Capped at
// MaxLineRange lines per call.
func ReadLines(path string, from, to int) ([]LineRange, error) {
	if from < 1 {
		return nil, fmt.Errorf("fileops.ReadLines: from=%d must be >= 1", from)
	}
	if to < from {
		return nil, fmt.Errorf("fileops.ReadLines: to=%d must be >= from=%d; lines are 1-based", to, from)
	}
	if to-from+1 > MaxLineRange {
		return nil, fmt.Errorf("fileops.ReadLines: range %d lines exceeds cap %d", to-from+1, MaxLineRange)
	}
	lines, err := readLines(path)
	if err != nil {
		return nil, err
	}
	total := len(lines)
	if from > total {
		return nil, fmt.Errorf("fileops.ReadLines: from=%d exceeds file length %d", from, total)
	}
	if to > total {
		to = total
	}
	out := make([]LineRange, 0, to-from+1)
	for i := from - 1; i < to; i++ {
		out = append(out, LineRange{Number: i + 1, Content: lines[i]})
	}
	return out, nil
}

// ReadContext reads radius lines around line (1-based).
// DefaultContextRadius is used when radius <= 0.
func ReadContext(path string, line, radius int) ([]LineRange, error) {
	if line < 1 {
		return nil, fmt.Errorf("fileops.ReadContext: line=%d must be >= 1", line)
	}
	if radius <= 0 {
		radius = DefaultContextRadius
	}
	if radius > MaxContextRadius {
		radius = MaxContextRadius
	}
	lines, err := readLines(path)
	if err != nil {
		return nil, err
	}
	total := len(lines)
	if line > total {
		return nil, fmt.Errorf("fileops.ReadContext: line=%d exceeds file length %d", line, total)
	}
	from := line - radius
	if from < 1 {
		from = 1
	}
	to := line + radius
	if to > total {
		to = total
	}
	out := make([]LineRange, 0, to-from+1)
	for i := from - 1; i < to; i++ {
		out = append(out, LineRange{Number: i + 1, Content: lines[i]})
	}
	return out, nil
}
