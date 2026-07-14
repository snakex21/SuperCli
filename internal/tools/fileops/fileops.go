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
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
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

	// AnchorSearchRadius is the ±N window EditLineAnchored
	// scans for the expected content when the hinted line
	// does not match verbatim (tolerates line drift).
	AnchorSearchRadius = 10
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
		return nil, fmt.Errorf("fileops.ReadLines: to=%d must be >= from=%d", to, from)
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

// EditLine replaces the content of line (1-based) with
// newContent. Returns a diff-style string showing ±3
// context lines around the change.
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
type WriteResult struct {
	// Created is true when the file did not exist before and was
	// newly created; false when an existing file was overwritten.
	Created bool
	// Bytes is the number of content bytes written.
	Bytes int
}

// WriteFile writes content to path, creating any missing parent
// directories. It reports whether the file was newly created or an
// existing one overwritten, plus the byte count — enough for the
// caller to state the change without re-reading the file.
//
// The package stays pure: WriteFile does NOT enforce the sandbox.
// Callers (the write_file tool) resolve the path with
// sandbox.ResolveSafe BEFORE calling this, exactly as ctx_execute
// does — keeping the safety boundary in one place (the tool).
func WriteFile(path, content string) (WriteResult, error) {
	if path == "" {
		return WriteResult{}, fmt.Errorf("fileops.WriteFile: empty path")
	}
	release := LockMutationPaths(path)
	defer release()
	_, statErr := os.Stat(path)
	existed := statErr == nil

	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return WriteResult{}, FileErr(err, dir)
		}
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return WriteResult{}, FileErr(err, path)
	}
	return WriteResult{Created: !existed, Bytes: len(content)}, nil
}

// MakeDir creates the directory at path, including any missing
// parents (like `mkdir -p`). It returns created=false when the
// directory already existed (idempotent, no error), and an error
// only when the path exists as a FILE or the mkdir fails.
//
// Like WriteFile, the package stays pure: the sandbox boundary is
// enforced by the calling tool via sandbox.ResolveSafe.
func MakeDir(path string) (created bool, err error) {
	if path == "" {
		return false, fmt.Errorf("fileops.MakeDir: empty path")
	}
	release := LockMutationPaths(path)
	defer release()
	if info, statErr := os.Stat(path); statErr == nil {
		if !info.IsDir() {
			return false, fmt.Errorf("fileops.MakeDir: %q exists and is a file, not a folder", path)
		}
		return false, nil // already a dir: idempotent success
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return false, fmt.Errorf("fileops.MakeDir: %w", err)
	}
	return true, nil
}

// Move renames/moves src to dst (works for files and folders, like
// `mv` / `git mv`). It is non-destructive by contract:
//
//   - if dst is an existing FOLDER, src is moved INTO it keeping its
//     base name (the familiar `mv file dir/` behaviour);
//   - it NEVER overwrites: if the final destination already exists,
//     it returns an error instead of clobbering — the caller should
//     ask the user how to proceed.
//
// Returns the final destination path actually used (after the
// move-into-folder adjustment) so the caller can report it.
// Pure: the sandbox is enforced by the tool on BOTH src and dst.
func Move(src, dst string) (finalDst string, err error) {
	if src == "" || dst == "" {
		return "", fmt.Errorf("fileops.Move: src and dst are required")
	}
	release := LockMutationPaths(src, dst)
	defer release()
	if _, err := os.Lstat(src); err != nil {
		return "", FileErr(err, src)
	}
	// move-INTO-folder convenience.
	if info, err := os.Lstat(dst); err == nil {
		if info.IsDir() && dst != src {
			dst = filepath.Join(dst, filepath.Base(src))
		}
	}
	// no-overwrite rule (re-check after the adjustment).
	if _, err := os.Lstat(dst); err == nil {
		return "", fmt.Errorf("fileops.Move: destination %q already exists; refusing to overwrite", dst)
	}
	if parent := filepath.Dir(dst); parent != "" {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return "", fmt.Errorf("fileops.Move: mkdir parent: %w", err)
		}
	}
	if err := os.Rename(src, dst); err != nil {
		return "", fmt.Errorf("fileops.Move: %w", err)
	}
	return dst, nil
}

// Copy copies src to dst (file or folder; folders are copied
// recursively, like `cp -r`). Same non-destructive contract as
// Move: if dst is an existing folder, src is copied INTO it; it
// NEVER overwrites an existing destination. Symlinks are skipped
// so a link target cannot smuggle data outside the tree.
//
// Returns the final destination path used. Pure: the tool enforces
// the sandbox on both src and dst.
func Copy(src, dst string) (finalDst string, err error) {
	if src == "" || dst == "" {
		return "", fmt.Errorf("fileops.Copy: src and dst are required")
	}
	release := LockMutationPaths(src, dst)
	defer release()
	srcInfo, err := os.Lstat(src)
	if err != nil {
		return "", FileErr(err, src)
	}
	if info, err := os.Lstat(dst); err == nil {
		if info.IsDir() && dst != src {
			dst = filepath.Join(dst, filepath.Base(src))
		}
	}
	if _, err := os.Lstat(dst); err == nil {
		return "", fmt.Errorf("fileops.Copy: destination %q already exists; refusing to overwrite", dst)
	}
	if srcInfo.IsDir() {
		if err := copyTree(src, dst); err != nil {
			return "", fmt.Errorf("fileops.Copy: %w", err)
		}
	} else {
		if err := copyFileContents(src, dst); err != nil {
			return "", fmt.Errorf("fileops.Copy: %w", err)
		}
	}
	return dst, nil
}

// copyTree copies a directory tree from src to dst. dst must not
// exist. Symlinks are skipped.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFileContents(p, target)
	})
}

// copyFileContents copies a single file's bytes from src to dst,
// creating dst's parent directory if needed.
func copyFileContents(src, dst string) error {
	if parent := filepath.Dir(dst); parent != "" {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return err
		}
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Trash moves src into trashDir under a timestamped name, instead
// of deleting it — so removal is always recoverable (the user can
// move it back out). Name collisions within the same second are
// disambiguated with a counter suffix. now is injected so the
// timestamp is testable (no hidden clock dependency).
//
// Returns the path the item was moved to. Pure: the tool resolves
// src against the sandbox and supplies trashDir under home.
func Trash(src, trashDir string, now time.Time) (dst string, err error) {
	if src == "" {
		return "", fmt.Errorf("fileops.Trash: empty path")
	}
	release := LockMutationPaths(src, trashDir)
	defer release()
	if _, err := os.Lstat(src); err != nil {
		return "", FileErr(err, src)
	}
	if err := os.MkdirAll(trashDir, 0o755); err != nil {
		return "", fmt.Errorf("fileops.Trash: prepare trash folder: %w", err)
	}
	stamp := now.Format("20060102-150405")
	base := filepath.Base(src)
	dst = filepath.Join(trashDir, stamp+"_"+base)
	for i := 1; ; i++ {
		if _, err := os.Lstat(dst); err != nil {
			break
		}
		dst = filepath.Join(trashDir, fmt.Sprintf("%s-%d_%s", stamp, i, base))
	}
	if err := os.Rename(src, dst); err != nil {
		return "", fmt.Errorf("fileops.Trash: %w", err)
	}
	return dst, nil
}
