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
// a bounded excerpt near the first difference; this is not a complete diff.
func patchWrittenSnapshot(before, after string) string {
	if after == "" {
		return "[Written snapshot: empty file]"
	}
	start := 0
	if len(after) > patchSnapshotBytes || strings.Count(after, "\n") > patchSnapshotLines {
		first := 0
		for first < len(before) && first < len(after) && before[first] == after[first] {
			first++
		}
		// At a deletion at EOF, show the remaining last line instead of an
		// imaginary extra line after the final newline.
		first = min(first, len(after)-1)
		start = strings.LastIndexByte(after[:first], '\n') + 1
		for context := 0; context < 2 && start > 0; context++ {
			start = strings.LastIndexByte(after[:start-1], '\n') + 1
		}
	}
	line := 1 + strings.Count(after[:start], "\n")
	var b strings.Builder
	b.Grow(min(patchSnapshotBytes, len(after)+128))
	b.WriteString("[Written snapshot]\n")
	const footerReserve = 96
	const truncatedLine = " ... [line truncated]\n"
	offset, shown, truncated := start, 0, false
	for offset < len(after) && shown < patchSnapshotLines {
		end := len(after)
		if n := strings.IndexByte(after[offset:], '\n'); n >= 0 {
			end = offset + n
		}
		body := strings.TrimSuffix(after[offset:end], "\r")
		prefix := fmt.Sprintf("%4d | ", line)
		available := patchSnapshotBytes - footerReserve - b.Len() - len(prefix) - 1
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
	if start > 0 || offset < len(after) || truncated {
		b.WriteString("[Partial snapshot; other content omitted]")
	} else {
		fmt.Fprintf(&b, "[end of file at line %d]", line-1)
	}
	return b.String()
}
