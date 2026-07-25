package fileops

import (
	"fmt"
	"strings"
)

// Failure diagnostics for patch_file.
//
// A failed patch costs the model everything it spent generating `old`
// (forensics: 41.8% of all mutation tokens were thrown away by calls that
// reported only "found 0"). The content was almost never wrong — the calls
// missed on whitespace or drifted in the tail. So the repair information is
// computed HERE, on the error path only: it costs nothing when the patch
// works, and it turns a full regeneration into a small correction.
//
// Everything below runs on data already in memory (the file was read to try
// the match) and only after the call has already failed.

const (
	// diagMaxContent skips diagnostics on very large files. The prefix
	// search is logarithmic, but the whitespace normalisations allocate a
	// copy of the file, so cap the work. Real source files are far below
	// this; a 4 MiB file is not something a patch failure should stall on.
	diagMaxContent = 4 << 20

	// diagMinPrefix and diagPrefixRatio decide when a matching prefix is a
	// near miss rather than noise. 12 chars of "func " + newlines matching
	// says nothing; a prefix that is both long in absolute terms and a
	// quarter of `old` means the model had the right block and drifted.
	diagMinPrefix   = 16
	diagPrefixRatio = 4

	// diagMinStripped guards the whitespace verdict: after removing all
	// whitespace a very short `old` matches by accident.
	diagMinStripped = 8

	// diagSnippet caps any file text echoed back into the error.
	diagSnippet = 120
)

// patchFailureHint returns the diagnostic tail appended to a PatchFile
// occurrence-count failure, already prefixed with "; ", or "" when nothing
// useful can be said. content is the file as it stands when the change was
// attempted (earlier changes in the same call already applied in memory).
func patchFailureHint(content, old string, idx, want, got int) string {
	parts := make([]string, 0, 4)

	// Which changes were fine. Lets the model resend the one that missed
	// instead of regenerating the whole patch.
	if idx > 0 {
		parts = append(parts, fmt.Sprintf(
			"changes 0-%d matched, change %d did not: resend change %d alone",
			idx-1, idx, idx))
	}

	switch {
	case got == 0:
		parts = append(parts, diagnoseMissingOld(content, old)...)
	case got > want:
		parts = append(parts, fmt.Sprintf(
			"old occurs %d times: add surrounding context to make it unique, or set expected_count=%d",
			got, got))
	}
	if len(parts) == 0 {
		return ""
	}
	return "; " + strings.Join(parts, "; ")
}

// diagnoseMissingOld explains why an `old` that the model believed was in the
// file was not found verbatim. It reports at most one structural verdict
// (whitespace vs. tail divergence) plus the single-line fact, in that order of
// usefulness.
func diagnoseMissingOld(content, old string) []string {
	if len(content) > diagMaxContent || old == "" {
		return nil
	}
	parts := make([]string, 0, 2)

	if ws := whitespaceVerdict(content, old); ws != "" {
		// Whitespace fully explains the miss; a divergence offset on top of
		// it would just be a second way of saying the same thing.
		parts = append(parts, ws)
	} else if pf := prefixVerdict(content, old); pf != "" {
		parts = append(parts, pf)
	}

	if isSingleLine(content) {
		parts = append(parts, fmt.Sprintf(
			"file is a single line of %d chars: old must be an exact byte run inside it",
			len(strings.TrimSuffix(content, "\n"))))
	}
	return parts
}

// whitespaceVerdict reports whether `old` would match if whitespace were
// normalised. It never changes the matching itself — patch_file stays exact —
// it only names the difference so the model can re-copy the real bytes.
func whitespaceVerdict(content, old string) string {
	squeezedOld := squeezeSpace(old)
	if len(squeezedOld) >= diagMinStripped {
		if n := strings.Count(squeezeSpace(content), squeezedOld); n > 0 {
			return fmt.Sprintf(
				"old matches %d time(s) once whitespace runs are collapsed: your indentation or line breaks differ from the file",
				n)
		}
	}
	strippedOld := stripSpace(old)
	if len(strippedOld) < diagMinStripped {
		return ""
	}
	if n := strings.Count(stripSpace(content), strippedOld); n > 0 {
		return fmt.Sprintf(
			"old matches %d time(s) with all whitespace removed: only whitespace differs, copy the exact bytes from read_lines",
			n)
	}
	return ""
}

// prefixVerdict reports the longest literal prefix of `old` present in the
// file and the exact point where the two diverge, so the model can fix the
// tail instead of regenerating the block.
func prefixVerdict(content, old string) string {
	n, pos := longestPrefixMatch(content, old)
	if n < diagMinPrefix || n*diagPrefixRatio < len(old) {
		return ""
	}
	startLine := lineOf(content, pos)
	divLine := lineOf(content, pos+n)
	return fmt.Sprintf(
		"first %d of %d chars of old match at line %d; old diverges at its line %d from file line %d, which reads: %q",
		n, len(old), startLine, lineOf(old, n), divLine, snippet(lineAt(content, pos+n)))
}

// longestPrefixMatch returns the length of the longest prefix of old that
// occurs in content, and the offset of its first occurrence (-1 if none).
//
// Presence is monotone — if old[:k] occurs, so does every shorter prefix, as a
// prefix of the same substring — so a binary search over the length is exact.
// That is ~log2(len(old)) calls to strings.Index (≈12 for a 2.5 KB `old`)
// instead of the O(n*m) scan a naive longest-common-substring would cost.
func longestPrefixMatch(content, old string) (int, int) {
	lo, hi := 0, len(old) // lo: known present, hi: known absent
	bestPos := 0
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		if at := strings.Index(content, old[:mid]); at >= 0 {
			lo, bestPos = mid, at
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return 0, -1
	}
	return lo, bestPos
}

// lineOf returns the 1-based line number containing byte offset off.
func lineOf(s string, off int) int {
	if off > len(s) {
		off = len(s)
	}
	if off < 0 {
		off = 0
	}
	return 1 + strings.Count(s[:off], "\n")
}

// lineAt returns the whole line of s containing byte offset off.
func lineAt(s string, off int) string {
	if off >= len(s) {
		off = len(s) - 1
	}
	if off < 0 || len(s) == 0 {
		return ""
	}
	start := strings.LastIndexByte(s[:off+1], '\n') + 1
	end := strings.IndexByte(s[start:], '\n')
	if end < 0 {
		return s[start:]
	}
	return s[start : start+end]
}

func snippet(s string) string {
	if len(s) <= diagSnippet {
		return s
	}
	return s[:diagSnippet] + "..."
}

// isSingleLine reports whether the file holds exactly one line (optionally
// newline-terminated). Every patch against such a file has to reproduce a byte
// run inside that one line, which is worth saying out loud — minified JS/CSS
// and one-line HTML were two of the four observed failures.
func isSingleLine(content string) bool {
	trimmed := strings.TrimSuffix(content, "\n")
	return trimmed != "" && !strings.Contains(trimmed, "\n")
}

// squeezeSpace collapses every run of whitespace to a single space and trims
// the ends. Detects "same text, different indentation/line breaks".
func squeezeSpace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inSpace := false
	for i := 0; i < len(s); i++ {
		if isSpaceByte(s[i]) {
			inSpace = true
			continue
		}
		if inSpace && b.Len() > 0 {
			b.WriteByte(' ')
		}
		inSpace = false
		b.WriteByte(s[i])
	}
	return b.String()
}

// stripSpace removes every whitespace byte. Detects the harder case where
// even the presence of a separator differs.
func stripSpace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if !isSpaceByte(s[i]) {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}
