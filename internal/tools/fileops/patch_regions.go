package fileops

import (
	"fmt"
	"sort"
	"strings"
)

// Changed-region tracking for the post-patch snapshot. A head/tail snapshot of
// a multi-replacement edit hid the middle replacements ("[... middle content
// omitted ...]"), which is exactly where an over-broad edit shows up (eval
// json-config-precision: four sections changed, two of them invisible). The
// patch already knows where every replacement landed, so it reports each one.

const (
	// Several regions share a larger budget than the single-region snapshot:
	// each replacement is small and every one of them must be visible.
	patchRegionsBytes = 2048
	patchRegionsLines = 32
	// Lines shown above each region; they usually name the enclosing section.
	patchRegionContext = 2
	// Rows of one region beyond this are elided; the region's line is named.
	patchRegionMaxRows = 6
)

// textRegion is a byte range of the current content produced by a replacement.
type textRegion struct{ start, end int }

// spliceTracked applies non-overlapping, ordered spans and returns the new
// content with regions remapped and the spans' replacements added as regions.
func spliceTracked(content string, spans []patchSpan, regions []textRegion) (string, []textRegion) {
	// Spans are ordered and non-empty (old is never empty).
	mapPos := func(p int, isEnd bool) int {
		delta := 0
		for _, sp := range spans {
			switch {
			case p <= sp.start:
				return p + delta
			case p < sp.end:
				// Inside a replaced span: widen to its replacement.
				if isEnd {
					return sp.start + delta + len(sp.text)
				}
				return sp.start + delta
			}
			delta += len(sp.text) - (sp.end - sp.start)
		}
		return p + delta
	}
	out := make([]textRegion, 0, len(regions)+len(spans))
	for _, r := range regions {
		out = append(out, textRegion{mapPos(r.start, false), mapPos(r.end, true)})
	}
	delta := 0
	for _, sp := range spans {
		start := sp.start + delta
		out = append(out, textRegion{start, start + len(sp.text)})
		delta += len(sp.text) - (sp.end - sp.start)
	}
	return splice(content, spans), out
}

// exactSpans locates want non-overlapping occurrences of old, in order.
func exactSpans(content, old, new string, want int) []patchSpan {
	spans := make([]patchSpan, 0, want)
	for off := 0; len(spans) < want; {
		i := strings.Index(content[off:], old)
		if i < 0 {
			break
		}
		start := off + i
		spans = append(spans, patchSpan{start: start, end: start + len(old), text: new})
		off = start + len(old)
	}
	return spans
}

// regionLines converts byte regions to merged 1-based line ranges.
func regionLines(content string, regions []textRegion) [][2]int {
	ranges := make([][2]int, 0, len(regions))
	for _, r := range regions {
		start := max(0, min(r.start, len(content)))
		end := max(start, min(r.end, len(content)))
		first := lineOf(content, start)
		last := first
		if end > start {
			last = lineOf(content, end-1)
		}
		ranges = append(ranges, [2]int{first, last})
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i][0] < ranges[j][0] })
	merged := ranges[:0]
	for _, r := range ranges {
		// Regions whose context windows touch are shown as one.
		if n := len(merged); n > 0 && r[0]-patchRegionContext <= merged[n-1][1]+1 {
			merged[n-1][1] = max(merged[n-1][1], r[1])
			continue
		}
		merged = append(merged, r)
	}
	return merged
}

// patchRegionsSnapshot shows every changed region of after with a little
// context above it. Regions that do not fit the budget are still named by
// line, so the model never mistakes an unseen replacement for no change.
func patchRegionsSnapshot(after string, ranges [][2]int) string {
	lines := strings.Split(after, "\n")
	var b strings.Builder
	fmt.Fprintf(&b, "[Written snapshot: %d changed regions]\n", len(ranges))
	rows := 0
	for i, r := range ranges {
		from := max(1, r[0]-patchRegionContext)
		to := min(r[1], len(lines))
		if to-from+1 > patchRegionMaxRows {
			to = from + patchRegionMaxRows - 1
		}
		var block strings.Builder
		for n := from; n <= to; n++ {
			text := strings.TrimSuffix(lines[n-1], "\r")
			if len(text) > diagSnippet {
				cut := diagSnippet
				for cut > 0 && text[cut]&0xC0 == 0x80 {
					cut--
				}
				text = text[:cut] + " ... [line truncated]"
			}
			fmt.Fprintf(&block, "%4d | %s\n", n, text)
		}
		if to < r[1] {
			fmt.Fprintf(&block, "[... region continues to line %d]\n", r[1])
		}
		if b.Len()+block.Len() > patchRegionsBytes-160 || rows+(to-from+1) > patchRegionsLines {
			rest := make([]string, 0, len(ranges)-i)
			for _, left := range ranges[i:] {
				rest = append(rest, fmt.Sprint(left[0]))
			}
			fmt.Fprintf(&b, "[%d more changed region(s) at lines %s not shown]\n", len(rest), strings.Join(rest, ","))
			break
		}
		if i > 0 {
			b.WriteString("[...]\n")
		}
		b.WriteString(block.String())
		rows += to - from + 1
	}
	b.WriteString("[Partial snapshot; unchanged content omitted]")
	return b.String()
}

// writtenPreview picks the snapshot: every region when the patch changed
// several separate places, otherwise the established single-region view.
func writtenPreview(before, after string, regions []textRegion) string {
	if after != "" && len(regions) > 1 {
		if ranges := regionLines(after, regions); len(ranges) > 1 {
			return patchRegionsSnapshot(after, ranges)
		}
	}
	return patchWrittenSnapshot(before, after)
}
