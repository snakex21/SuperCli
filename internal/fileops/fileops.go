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
	"fmt"
	"os"
	"strings"
)

const (
	// MaxLineRange caps read_lines to prevent abuse.
	MaxLineRange = 500

	// DefaultContextRadius is the default ±N lines for
	// read_context.
	DefaultContextRadius = 10

	// DiffContext is the number of context lines shown
	// before/after a mutation in the diff output.
	DiffContext = 3

	// AnchorSearchRadius is the ±N window EditLineAnchored
	// scans for the expected content when the hinted line
	// does not match verbatim (tolerates line drift).
	AnchorSearchRadius = 10
)

// readLines reads all lines from path. Returns the lines
// slice (0-indexed) and nil error on success. Lines do
// NOT include trailing newlines.
func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Handle empty file.
	if len(data) == 0 {
		return []string{}, nil
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
	Number int    `json:"number"`
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
		return nil, fmt.Errorf("fileops.ReadLines: to=%d must be >= from=%d", to, from)
	}
	if to-from+1 > MaxLineRange {
		return nil, fmt.Errorf("fileops.ReadLines: range %d lines exceeds cap %d", to-from+1, MaxLineRange)
	}
	lines, err := readLines(path)
	if err != nil {
		return nil, fmt.Errorf("fileops.ReadLines: %w", err)
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
	lines, err := readLines(path)
	if err != nil {
		return nil, fmt.Errorf("fileops.ReadContext: %w", err)
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

// EditLine replaces the content of line (1-based) with
// newContent. Returns a diff-style string showing ±3
// context lines around the change.
func EditLine(path string, line int, newContent string) (string, error) {
	if line < 1 {
		return "", fmt.Errorf("fileops.EditLine: line=%d must be >= 1", line)
	}
	lines, err := readLines(path)
	if err != nil {
		return "", fmt.Errorf("fileops.EditLine: %w", err)
	}
	total := len(lines)
	if line > total {
		return "", fmt.Errorf("fileops.EditLine: line=%d exceeds file length %d", line, total)
	}
	old := lines[line-1]
	lines[line-1] = newContent
	if err := writeLines(path, lines); err != nil {
		return "", fmt.Errorf("fileops.EditLine: write: %w", err)
	}
	return buildDiff(lines, line-1, old, newContent), nil
}

// EditLineAnchored replaces a line whose content is proven by
// expectedOld, using line as a GPS hint rather than a blind
// target. The hinted line wins if it matches expectedOld
// verbatim; otherwise the function scans ±AnchorSearchRadius
// for the content. This neutralises the two failure modes of
// line-only editing at once:
//
//   - drift: when imports/edits above shift the target, the
//     content match still finds it within the window;
//   - ambiguity: when the same content repeats, the hint line
//     disambiguates which occurrence to touch.
//
// It fails LOUDLY instead of corrupting the file:
//
//   - no occurrence of expectedOld within the window → error,
//     nothing written;
//   - 2+ occurrences within the window and none on the hinted
//     line → error asking for more context, nothing written.
//
// Returns the same ±DiffContext diff as EditLine on success.
func EditLineAnchored(path string, line int, expectedOld, newContent string) (string, error) {
	if line < 1 {
		return "", fmt.Errorf("fileops.EditLineAnchored: line=%d must be >= 1", line)
	}
	if expectedOld == "" {
		return "", fmt.Errorf("fileops.EditLineAnchored: expectedOld must be non-empty (anchor needs proof content)")
	}
	lines, err := readLines(path)
	if err != nil {
		return "", fmt.Errorf("fileops.EditLineAnchored: %w", err)
	}
	total := len(lines)
	if total == 0 {
		return "", fmt.Errorf("fileops.EditLineAnchored: file is empty, cannot anchor %q", expectedOld)
	}

	// Fast path: the hint is exact. Trust it even if the same
	// content repeats elsewhere — the model pointed here.
	if line <= total && lines[line-1] == expectedOld {
		return applyAnchoredEdit(path, lines, line-1, expectedOld, newContent)
	}

	// Drift path: scan ±AnchorSearchRadius around the hint for
	// a verbatim match. Clamp the window to the file bounds.
	from := line - AnchorSearchRadius
	if from < 1 {
		from = 1
	}
	to := line + AnchorSearchRadius
	if to > total {
		to = total
	}
	matches := make([]int, 0, 2) // 0-based indices
	for i := from - 1; i < to; i++ {
		if lines[i] == expectedOld {
			matches = append(matches, i)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf(
			"fileops.EditLineAnchored: expected content not found within %d lines of line %d; nothing changed (expected %q)",
			AnchorSearchRadius, line, expectedOld)
	case 1:
		return applyAnchoredEdit(path, lines, matches[0], expectedOld, newContent)
	default:
		nums := make([]int, len(matches))
		for i, idx := range matches {
			nums[i] = idx + 1
		}
		return "", fmt.Errorf(
			"fileops.EditLineAnchored: content is ambiguous near line %d (matches lines %v); provide more context; nothing changed",
			line, nums)
	}
}

// applyAnchoredEdit writes the replacement at the resolved
// 0-based index and returns the verification diff. Shared by
// the exact-hint and drift paths of EditLineAnchored.
func applyAnchoredEdit(path string, lines []string, at int, oldContent, newContent string) (string, error) {
	lines[at] = newContent
	if err := writeLines(path, lines); err != nil {
		return "", fmt.Errorf("fileops.EditLineAnchored: write: %w", err)
	}
	return buildDiff(lines, at, oldContent, newContent), nil
}

// InsertAfter inserts newContent as a new line after line
// (1-based). Returns a diff-style string showing ±3
// context lines around the insertion point.
func InsertAfter(path string, line int, newContent string) (string, error) {
	if line < 1 {
		return "", fmt.Errorf("fileops.InsertAfter: line=%d must be >= 1", line)
	}
	lines, err := readLines(path)
	if err != nil {
		return "", fmt.Errorf("fileops.InsertAfter: %w", err)
	}
	total := len(lines)
	if line > total {
		return "", fmt.Errorf("fileops.InsertAfter: line=%d exceeds file length %d", line, total)
	}
	// Insert after line (1-based) = insert at index line
	// in the 0-based slice.
	newLines := make([]string, 0, len(lines)+1)
	newLines = append(newLines, lines[:line]...)
	newLines = append(newLines, newContent)
	newLines = append(newLines, lines[line:]...)
	if err := writeLines(path, newLines); err != nil {
		return "", fmt.Errorf("fileops.InsertAfter: write: %w", err)
	}
	return buildDiff(newLines, line, "", newContent), nil
}

// DeleteLines removes lines from..to (inclusive, 1-based).
// Returns a diff-style string showing ±3 context lines
// around the deletion point.
func DeleteLines(path string, from, to int) (string, error) {
	if from < 1 {
		return "", fmt.Errorf("fileops.DeleteLines: from=%d must be >= 1", from)
	}
	if to < from {
		return "", fmt.Errorf("fileops.DeleteLines: to=%d must be >= from=%d", to, from)
	}
	lines, err := readLines(path)
	if err != nil {
		return "", fmt.Errorf("fileops.DeleteLines: %w", err)
	}
	total := len(lines)
	if from > total {
		return "", fmt.Errorf("fileops.DeleteLines: from=%d exceeds file length %d", from, total)
	}
	if to > total {
		to = total
	}
	// Build the diff BEFORE mutating. Show the deleted
	// lines as removals.
	diff := buildDeleteDiff(lines, from-1, to-1)
	// Splice out the range.
	newLines := make([]string, 0, len(lines)-(to-from+1))
	newLines = append(newLines, lines[:from-1]...)
	newLines = append(newLines, lines[to:]...)
	if err := writeLines(path, newLines); err != nil {
		return "", fmt.Errorf("fileops.DeleteLines: write: %w", err)
	}
	return diff, nil
}

// --- diff builders ---

// buildDiff produces a unified-diff-style string showing
// the change at index `at` with ±DiffContext context lines.
// oldLine is the removed line; newLine is the added line.
// When oldLine is "" (insertion), only + is shown.
func buildDiff(lines []string, at int, oldLine, newLine string) string {
	var b strings.Builder
	from := at - DiffContext
	if from < 0 {
		from = 0
	}
	to := at + DiffContext
	if to >= len(lines) {
		to = len(lines) - 1
	}
	for i := from; i <= to; i++ {
		prefix := " "
	Content := lines[i]
		if i == at {
			if oldLine != "" {
				prefix = "-"
				fmt.Fprintf(&b, "%s %3d: %s\n", prefix, i+1, oldLine)
				prefix = "+"
			}
			fmt.Fprintf(&b, "%s %3d: %s\n", prefix, i+1, newLine)
		} else {
			_ = Content
			fmt.Fprintf(&b, "%s %3d: %s\n", prefix, i+1, lines[i])
		}
	}
	return b.String()
}

// buildDeleteDiff produces a diff showing deleted lines
// (marked with -) and ±DiffContext context.
func buildDeleteDiff(lines []string, from, to int) string {
	var b strings.Builder
	ctxFrom := from - DiffContext
	if ctxFrom < 0 {
		ctxFrom = 0
	}
	ctxTo := to + DiffContext
	if ctxTo >= len(lines) {
		ctxTo = len(lines) - 1
	}
	for i := ctxFrom; i <= ctxTo; i++ {
		if i >= from && i <= to {
			fmt.Fprintf(&b, "- %3d: %s\n", i+1, lines[i])
		} else {
			fmt.Fprintf(&b, "  %3d: %s\n", i+1, lines[i])
		}
	}
	return b.String()
}
