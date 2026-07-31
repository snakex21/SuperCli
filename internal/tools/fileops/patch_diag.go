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

	// diagForeignMinOld and diagForeignRatio are the mirror of the two above:
	// they decide when a prefix is so short that `old` cannot have come from
	// this file at all. The observed failure had a 4-char prefix (indentation)
	// against len(old)=453 — the strongest possible evidence that the model
	// aimed at the wrong path, and exactly what diagMinPrefix threw away as
	// noise. Below diagForeignMinOld bytes a short prefix is unremarkable, so
	// the verdict stays quiet; above it, a prefix under len(old)/20 (5%) is
	// far outside anything a typo produces — a single wrong character leaves a
	// prefix averaging half of `old`, and even a mistake in the first line
	// leaves the interior intact, which the probes below check for.
	diagForeignMinOld = 200
	diagForeignRatio  = 20

	// diagProbeLen and diagProbes sample the interior of `old` to confirm the
	// foreign verdict. A block that really belongs to the file leaves whole
	// windows of itself in it even when its head is wrong; a block from
	// another file leaves none. Four 32-byte windows cost four strings.Index
	// scans, roughly a third of the prefix binary search already spent.
	diagProbeLen = 32
	diagProbes   = 4

	// diagProbeMinInk is how much non-whitespace a probe must carry to count.
	// A window of pure indentation matches almost any source file and would
	// silence the verdict for the wrong reason.
	diagProbeMinInk = 16

	// diagMaxCrossChanges bounds the whole-call scan, which costs one pass
	// over the file per change. Patches that miss everywhere are 2-5 changes
	// in practice; past this the verdict is not worth a linear-in-changes
	// scan of a file that may be megabytes.
	diagMaxCrossChanges = 32

	// diagSnippet caps any file text echoed back into the error.
	diagSnippet = 120

	// diagMaxOccLines caps the line list for an ambiguous `old`. Ten numbers
	// are enough to pick a disambiguating anchor; a boilerplate string that
	// occurs 400 times must not paste 400 numbers into the error.
	diagMaxOccLines = 10
)

// patchFailureHint returns the diagnostic tail appended to a PatchFile
// occurrence-count failure, already prefixed with "; ", or "" when nothing
// useful can be said. content is the file as it stands when the change was
// attempted (earlier changes in the same call already applied in memory);
// changes is the whole call, idx the change that failed.
func patchFailureHint(path, content string, changes []PatchChange, idx, want, got int) string {
	if idx < 0 || idx >= len(changes) {
		return ""
	}
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
		parts = append(parts, diagnoseMissingOld(path, content, changes, idx)...)
	case got > want:
		parts = append(parts, fmt.Sprintf(
			"old occurs %d times%s: add surrounding context to make it unique, or set expected_count=%d",
			got, occurrenceLines(content, changes[idx].Old), got))
	}
	if len(parts) == 0 {
		return ""
	}
	return "; " + strings.Join(parts, "; ")
}

// diagnoseMissingOld explains why an `old` that the model believed was in the
// file was not found verbatim. It reports at most one structural verdict plus
// the single-line fact. The verdicts are mutually exclusive by construction and
// ordered by how much they narrow the repair:
//
//	whitespace   the text is here, only spacing differs — re-copy the bytes
//	near miss    the block is here and drifted — fix the tail
//	wrong file   nothing of this patch is here — fix the path
//
// The last one has to come last: whitespace and near-miss both prove the model
// is looking at the right file, and telling it to go check the path then would
// send a correctable call back to the drawing board.
func diagnoseMissingOld(path, content string, changes []PatchChange, idx int) []string {
	old := changes[idx].Old
	if len(content) > diagMaxContent || old == "" {
		return nil
	}
	parts := make([]string, 0, 3)

	if ws := whitespaceVerdict(content, old); ws != "" {
		// Whitespace fully explains the miss; a divergence offset on top of
		// it would just be a second way of saying the same thing.
		parts = append(parts, ws)
	} else {
		// One prefix search feeds both remaining verdicts: they read the same
		// number from opposite ends ("nearly all of old is here" vs. "almost
		// none of it is"), and it is the most expensive measurement taken.
		n, pos := longestPrefixMatch(content, old)
		if pf := prefixVerdict(content, old, n, pos); pf != "" {
			parts = append(parts, pf)
		} else if wf := wrongFileVerdicts(content, changes, idx, n); len(wf) > 0 {
			parts = append(parts, wf...)
		}
	}

	if isSingleLine(content) {
		parts = append(parts, fmt.Sprintf(
			"file is a single line of %d chars: old must be an exact byte run inside it",
			len(strings.TrimSuffix(content, "\n"))))
	}
	return append(parts, staleReadVerdict(path, len(parts) == 0)...)
}

// staleReadVerdict says that the bytes the model matched against are older than
// the file. When this process has already patched the path, that is a fact, not
// a guess, and it is the likeliest cause of a miss the verdicts above cannot
// explain. Otherwise the only honest thing left is the cheapest instruction:
// look at the file again. It is emitted only when nothing else fired, so a call
// that already got a precise repair is not padded with generic advice.
func staleReadVerdict(path string, alone bool) []string {
	if n := patchWriteCount(path); n > 0 {
		return []string{fmt.Sprintf(
			"this file was patched %d time(s) earlier in this session: re-read it, old may predate your own edit", n)}
	}
	if alone {
		return []string{"re-read the file before patching"}
	}
	return nil
}

// occurrenceLines renders ", lines 3, 40, 51" for the matches of old, or "" when
// there are none to show. Which lines the duplicates are on is what turns "add
// surrounding context" from advice into an instruction the model can follow
// without another read. One pass over the file, on the failure path only.
func occurrenceLines(content, old string) string {
	if old == "" || len(content) > diagMaxContent {
		return ""
	}
	nums := make([]string, 0, diagMaxOccLines)
	line, off := 1, 0
	for off < len(content) {
		i := strings.Index(content[off:], old)
		if i < 0 {
			break
		}
		line += strings.Count(content[off:off+i], "\n")
		if len(nums) == diagMaxOccLines {
			nums = append(nums, "...")
			break
		}
		nums = append(nums, fmt.Sprint(line))
		line += strings.Count(old, "\n")
		off += i + len(old)
	}
	if len(nums) == 0 {
		return ""
	}
	return " at lines " + strings.Join(nums, ",")
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
// tail instead of regenerating the block. n and pos come from
// longestPrefixMatch.
func prefixVerdict(content, old string, n, pos int) string {
	if n < diagMinPrefix || n*diagPrefixRatio < len(old) {
		return ""
	}
	startLine := lineOf(content, pos)
	divLine := lineOf(content, pos+n)
	return fmt.Sprintf(
		"first %d of %d chars of old match at line %d; old diverges at its line %d from file line %d, which reads: %q",
		n, len(old), startLine, lineOf(old, n), divLine, snippet(lineAt(content, pos+n)))
}

// wrongFileVerdicts reports that the patch was aimed at the wrong path. It is
// reached only after whitespace and near-miss have both declined, i.e. nothing
// suggests the block belongs here.
//
// Two independent signals, cheapest first. They complement rather than repeat
// each other: one is about matching, the other is arithmetic.
func wrongFileVerdicts(content string, changes []PatchChange, idx, prefix int) []string {
	out := make([]string, 0, 2)

	// Whole-call signals only make sense while nothing has been applied yet.
	// Past idx 0 an earlier change matched, which settles that the path is
	// right, and `content` no longer is the file the caller sent bytes for.
	if idx == 0 {
		if len(changes) > 1 && len(changes) <= diagMaxCrossChanges &&
			noChangeMatches(content, changes, idx) {
			out = append(out, fmt.Sprintf(
				"none of the %d changes match this file at all - the path is probably wrong",
				len(changes)))
		}
		// Non-overlapping occurrences cannot exceed the file, so `old` bytes
		// totalling more than it is a contradiction visible without any
		// search. expected_count is deliberately ignored: counting it would
		// only make the check fire more often, and it must never over-claim.
		total := 0
		for _, ch := range changes {
			total += len(ch.Old)
		}
		if total > len(content) {
			if len(changes) == 1 {
				out = append(out, fmt.Sprintf(
					"old is %d bytes but the file is only %d bytes: it cannot be in there",
					total, len(content)))
			} else {
				out = append(out, fmt.Sprintf(
					"changes total %d bytes of old but the file is only %d bytes: they cannot all be in it",
					total, len(content)))
			}
		}
	}
	if len(out) > 0 {
		return out
	}
	if fo := foreignOldVerdict(content, changes[idx].Old, prefix); fo != "" {
		out = append(out, fo)
	}
	return out
}

// noChangeMatches reports whether every change in the call misses. One block
// gone astray is drift; the whole patch missing is a statement about the path.
// Change idx is skipped: it is why we are here, and its count is already known.
func noChangeMatches(content string, changes []PatchChange, idx int) bool {
	for i, ch := range changes {
		if i == idx {
			continue
		}
		if ch.Old == "" || strings.Contains(content, ch.Old) {
			return false
		}
	}
	return true
}

// foreignOldVerdict reports a single `old` that has essentially nothing in
// common with the file. This is the per-change form of the verdict: it carries
// calls where the whole-call signals cannot speak — a lone change, or a patch
// that mixes blocks from two files.
// n is the longest-prefix length already measured for old.
func foreignOldVerdict(content, old string, n int) string {
	if len(old) < diagForeignMinOld || n*diagForeignRatio >= len(old) {
		return ""
	}
	if probeHits(content, old) > 0 {
		// Windows from the middle of `old` are in the file: the block does
		// belong here and something near its head is wrong. Not a path
		// problem, and not worth guessing about further.
		return ""
	}
	return fmt.Sprintf(
		"old shares almost nothing with this file (only %d of %d chars match anywhere) - check that path is the file this block came from",
		n, len(old))
}

// probeHits counts how many evenly spaced interior windows of old occur in
// content. Whitespace-only windows are skipped: they match anything.
func probeHits(content, old string) int {
	hits := 0
	for i := 1; i <= diagProbes; i++ {
		start := len(old) * i / (diagProbes + 1)
		if start+diagProbeLen > len(old) {
			start = len(old) - diagProbeLen
		}
		if start < 0 {
			continue
		}
		probe := old[start : start+diagProbeLen]
		if len(stripSpace(probe)) < diagProbeMinInk {
			continue
		}
		if strings.Contains(content, probe) {
			hits++
		}
	}
	return hits
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
