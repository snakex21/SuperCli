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
	"sort"
	"strings"
)

func EditLine(path string, line int, newContent string) (string, error) {
	if line < 1 {
		return "", fmt.Errorf("fileops.EditLine: line=%d must be >= 1", line)
	}
	release := LockMutationPaths(path)
	defer release()
	lines, err := readLines(path)
	if err != nil {
		return "", err
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
	release := LockMutationPaths(path)
	defer release()
	lines, err := readLines(path)
	if err != nil {
		return "", err
	}
	at, err := resolveAnchor(lines, line, expectedOld)
	if err != nil {
		return "", fmt.Errorf("fileops.EditLineAnchored: %w; nothing changed", err)
	}
	actualOld := lines[at]
	// Local models commonly copy the visible line text but omit its leading
	// tab/spaces. When that is the ONLY mismatch, preserve the file's actual
	// indentation in the replacement. Content after indentation remains an
	// exact anchor, so this cannot silently turn a fuzzy semantic match into an
	// edit.
	if actualOld != expectedOld && sameIgnoringIndent(actualOld, expectedOld) {
		actualIndent := leadingIndent(actualOld)
		expectedIndent := leadingIndent(expectedOld)
		if leadingIndent(newContent) == expectedIndent {
			newContent = actualIndent + strings.TrimPrefix(newContent, expectedIndent)
		}
	}
	return applyAnchoredEdit(path, lines, at, actualOld, newContent)
}

// resolveAnchor resolves a content anchor to a concrete 0-based
// index in lines, applying the same policy as EditLineAnchored:
// trust an exact hint, else scan ±AnchorSearchRadius for a
// verbatim match, failing loudly on no-match or ambiguity. It
// does NOT mutate or write — callers use it to validate edits
// before touching the file. The returned error is descriptive
// and safe to surface to the model.
func resolveAnchor(lines []string, line int, expectedOld string) (int, error) {
	if line < 1 {
		return 0, fmt.Errorf("line=%d must be >= 1", line)
	}
	if expectedOld == "" {
		return 0, fmt.Errorf("expectedOld must be non-empty (anchor needs proof content)")
	}
	total := len(lines)
	if total == 0 {
		return 0, fmt.Errorf("file is empty, cannot anchor %q", expectedOld)
	}
	// Fast path: the hint is exact. Trust it even if the same
	// content repeats elsewhere — the model pointed here.
	if line <= total && lines[line-1] == expectedOld {
		return line - 1, nil
	}
	// Safe local-model tolerance: a line at the exact hint may differ only in
	// leading indentation. The non-whitespace content still has to be exact.
	if line <= total && sameIgnoringIndent(lines[line-1], expectedOld) {
		return line - 1, nil
	}
	// Drift path: scan ±AnchorSearchRadius around the hint for a
	// verbatim match. Clamp the window to the file bounds.
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
	if len(matches) == 0 {
		for i := from - 1; i < to; i++ {
			if sameIgnoringIndent(lines[i], expectedOld) {
				matches = append(matches, i)
			}
		}
	}
	switch len(matches) {
	case 0:
		return 0, fmt.Errorf(
			"expected content not found within %d lines of line %d (expected %q)",
			AnchorSearchRadius, line, expectedOld)
	case 1:
		return matches[0], nil
	default:
		nums := make([]int, len(matches))
		for i, idx := range matches {
			nums[i] = idx + 1
		}
		return 0, fmt.Errorf(
			"content is ambiguous near line %d (matches lines %v); provide more context",
			line, nums)
	}
}

func trimIndent(s string) string { return strings.TrimLeft(s, " \t") }

func leadingIndent(s string) string { return s[:len(s)-len(trimIndent(s))] }

func sameIgnoringIndent(actual, expected string) bool {
	trimmed := trimIndent(expected)
	return trimmed != "" && trimIndent(actual) == trimmed
}

// AnchoredEdit is one content-anchored replacement: replace the
// line proven by ExpectedOld (located via Line as a hint) with
// NewContent. Used by EditLinesAnchored for batch edits.
type AnchoredEdit struct {
	Line        int
	ExpectedOld string
	NewContent  string
}

// EditLinesAnchored applies several content-anchored edits to a
// file in ONE call, atomically and bottom-to-top.
//
// Per ROADMAP item #4: edits are applied from the highest line
// number down so an earlier edit never shifts the line numbers of
// a later one. Every anchor is resolved and validated against the
// CURRENT file BEFORE anything is written, so a single bad anchor
// fails the whole call and leaves the file untouched (all-or-
// nothing) — never a half-applied, drifted edit.
//
// Validation rules (same policy as EditLineAnchored):
//   - each edit must supply non-empty ExpectedOld;
//   - each anchor must match verbatim on its hinted line or
//     within ±AnchorSearchRadius, else the call fails;
//   - two edits resolving to the SAME line fail (conflicting).
//
// Returns one concatenated ±DiffContext diff per applied edit,
// in top-to-bottom order for readability.
func EditLinesAnchored(path string, edits []AnchoredEdit) (string, error) {
	if len(edits) == 0 {
		return "", fmt.Errorf("fileops.EditLinesAnchored: no edits supplied")
	}
	release := LockMutationPaths(path)
	defer release()
	lines, err := readLines(path)
	if err != nil {
		return "", err
	}

	// Phase 1: resolve & validate ALL anchors against the
	// unmodified file. Nothing is written if any fails.
	type resolved struct {
		at         int // 0-based index
		oldContent string
		newContent string
	}
	out := make([]resolved, 0, len(edits))
	seen := make(map[int]int, len(edits)) // at -> edit ordinal
	for i, e := range edits {
		at, err := resolveAnchor(lines, e.Line, e.ExpectedOld)
		if err != nil {
			return "", fmt.Errorf("fileops.EditLinesAnchored: edit %d (line %d): %w; nothing changed", i+1, e.Line, err)
		}
		if prev, dup := seen[at]; dup {
			return "", fmt.Errorf(
				"fileops.EditLinesAnchored: edits %d and %d both target line %d; nothing changed",
				prev+1, i+1, at+1)
		}
		seen[at] = i
		out = append(out, resolved{at: at, oldContent: e.ExpectedOld, newContent: e.NewContent})
	}

	// Phase 2: apply bottom-to-top so indices stay valid. Since
	// every edit replaces exactly one line (no insert/delete),
	// indices do not actually shift here, but we honour the
	// ROADMAP ordering so this stays correct if range edits are
	// added later.
	sort.Slice(out, func(a, b int) bool { return out[a].at > out[b].at })
	for _, r := range out {
		lines[r.at] = r.newContent
	}
	if err := writeLines(path, lines); err != nil {
		return "", fmt.Errorf("fileops.EditLinesAnchored: write: %w", err)
	}

	// Build one diff per edit, top-to-bottom for human reading.
	sort.Slice(out, func(a, b int) bool { return out[a].at < out[b].at })
	var b strings.Builder
	for i, r := range out {
		if i > 0 {
			b.WriteString("---\n")
		}
		b.WriteString(buildDiff(lines, r.at, r.oldContent, r.newContent))
	}
	return b.String(), nil
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
	release := LockMutationPaths(path)
	defer release()
	lines, err := readLines(path)
	if err != nil {
		return "", err
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
	release := LockMutationPaths(path)
	defer release()
	lines, err := readLines(path)
	if err != nil {
		return "", err
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

// WriteResult reports the outcome of WriteFile so the tool layer
// can tell the model (and the user) exactly what happened.
