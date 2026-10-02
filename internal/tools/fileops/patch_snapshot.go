package fileops

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const patchSnapshotBytes = 1024
const patchSnapshotLines = 16

// patchWrittenSnapshot describes the bytes just written, never a subsequent
// disk read or a reconstruction from requested replacements. Large files show
// a bounded excerpt near the first difference. When the last difference is
// found cheaply near EOF, a second excerpt shares the same budget. This is
// always a partial snapshot, never a complete diff of every change.
func patchWrittenSnapshot(before, after string) string {
	if after == "" {
		return "[Written snapshot: empty file]"
	}
	start := 0
	if len(after) > patchSnapshotBytes || strings.Count(after, "\n") > patchSnapshotLines {
		first := 0
		if len(after) > patchSnapshotBytes {
			first = patchSnapshotPrefix(before, after)
		} else {
			for first < len(before) && first < len(after) && before[first] == after[first] {
				first++
			}
		}
		// At a deletion at EOF, show the remaining last line instead of an
		// imaginary extra line after the final newline.
		firstDisplay := min(first, len(after)-1)
		start = patchSnapshotStart(after, firstDisplay)
		// Tiny snapshots retain their original fast path.
		if len(after) > patchSnapshotBytes/2 {
			if last, ok := patchSnapshotLastDifference(before, after, first); ok {
				// Nearby differences already fit the ordinary snapshot. Do not
				// split one long/minified line into misleading separate windows.
				end := patchSnapshotLineEnd(after, start, patchSnapshotLines)
				if last >= end && last >= 0 {
					lastStart := patchSnapshotStart(after, last)
					if lastStart >= patchSnapshotLineEnd(after, start, patchSnapshotLines/2) {
						return patchSnapshotTwoRegions(after, start, lastStart)
					}
				}
			}
		}
	}
	line := 1 + strings.Count(after[:start], "\n")
	var b strings.Builder
	b.Grow(min(patchSnapshotBytes, len(after)+128))
	b.WriteString("[Written snapshot]\n")
	offset, shown, truncated := patchSnapshotWindow(&b, after, start, line, patchSnapshotLines, patchSnapshotBytes-96)
	if start > 0 || offset < len(after) || truncated {
		b.WriteString("[Partial snapshot; other content omitted]")
	} else {
		fmt.Fprintf(&b, "[end of file at line %d]", line+shown-1)
	}
	return b.String()
}

// Compare matching blocks with runtime memequal before locating a mismatching
// byte. A late edit must not pay a Go byte loop over an otherwise unchanged MB.
func patchSnapshotPrefix(before, after string) int {
	limit := min(len(before), len(after))
	first := 0
	const block = 4096
	for limit-first >= block && before[first:first+block] == after[first:first+block] {
		first += block
	}
	for first < limit && before[first] == after[first] {
		first++
	}
	return first
}

// Search only a bounded suffix. Scanning all of a large common suffix would
// make an ordinary edit near the beginning much slower just for presentation.
// A difference found while walking backwards is the exact final difference;
// an unchanged window simply leaves the normal first-region snapshot intact.
func patchSnapshotLastDifference(before, after string, first int) (int, bool) {
	remaining := min(len(before)-first, len(after)-first)
	if remaining <= 0 {
		return len(after) - 1, len(after) > first
	}
	limit := min(remaining, 4096)
	beforeEnd, afterEnd := len(before), len(after)
	if before[beforeEnd-limit:] == after[afterEnd-limit:] {
		// Exhausting the shorter remainder can expose an insertion just
		// before this suffix. Otherwise stop at the presentation-work cap.
		if remaining <= limit && afterEnd-first > limit {
			return afterEnd - limit - 1, true
		}
		return 0, false
	}
	matched := 0
	const block = 256
	for limit-matched >= block && before[beforeEnd-matched-block:beforeEnd-matched] == after[afterEnd-matched-block:afterEnd-matched] {
		matched += block
	}
	for matched < limit && before[beforeEnd-matched-1] == after[afterEnd-matched-1] {
		matched++
	}
	return afterEnd - matched - 1, true
}

func patchSnapshotStart(after string, position int) int {
	start := strings.LastIndexByte(after[:position], '\n') + 1
	changedLine := start
	for context := 0; context < 2 && start > 0; context++ {
		candidate := strings.LastIndexByte(after[:start-1], '\n') + 1
		// Long unchanged context must not consume a region before the changed
		// line is visible. Keep most of the shared budget for the actual result.
		if changedLine-candidate > patchSnapshotBytes/4 {
			break
		}
		start = candidate
	}
	return start
}

func patchSnapshotLineEnd(after string, start, lines int) int {
	for lines > 0 && start < len(after) {
		n := strings.IndexByte(after[start:], '\n')
		if n < 0 {
			return len(after)
		}
		start += n + 1
		lines--
	}
	return start
}

func patchSnapshotTwoRegions(after string, firstStart, lastStart int) string {
	var b strings.Builder
	b.Grow(patchSnapshotBytes)
	b.WriteString("[Written snapshot: first and last differing regions]\n")
	firstLine := 1 + strings.Count(after[:firstStart], "\n")
	// Reserve half of the byte budget for each region, including a long line
	// in either region. A minified first line cannot swallow the final one.
	patchSnapshotWindow(&b, after, firstStart, firstLine, patchSnapshotLines/2, patchSnapshotBytes/2)
	b.WriteString("[... middle content omitted ...]\n")
	lastLine := firstLine + strings.Count(after[firstStart:lastStart], "\n")
	patchSnapshotWindow(&b, after, lastStart, lastLine, patchSnapshotLines-patchSnapshotLines/2, patchSnapshotBytes-96)
	b.WriteString("[Partial snapshot; other content omitted]")
	return b.String()
}

// byteEnd is an absolute builder length, so separate windows share one strict
// cap without allocating intermediate strings. Every displayed row comes from
// after with its final line number, including insertions and deletions.
func patchSnapshotWindow(b *strings.Builder, after string, start, line, maxLines, byteEnd int) (offset, shown int, truncated bool) {
	const truncatedLine = " ... [line truncated]\n"
	offset = start
	for offset < len(after) && shown < maxLines {
		end := len(after)
		if n := strings.IndexByte(after[offset:], '\n'); n >= 0 {
			end = offset + n
		}
		body := strings.TrimSuffix(after[offset:end], "\r")
		prefix := fmt.Sprintf("%4d | ", line)
		available := byteEnd - b.Len() - len(prefix) - 1
		if available <= len(truncatedLine) {
			break
		}
		b.WriteString(prefix)
		if len(body) > available {
			limit := available - len(truncatedLine)
			for limit > 0 && !utf8.RuneStart(body[limit]) {
				limit--
			}
			b.WriteString(body[:limit])
			b.WriteString(truncatedLine)
			truncated = true
			break
		}
		b.WriteString(body)
		b.WriteByte('\n')
		offset = min(end+1, len(after))
		shown++
		line++
	}
	return offset, shown, truncated
}
