package search

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"supercli/internal/tools/core"
)

const maxSearchPreviewHits = core.ModelOutputPreviewBytes / 64

type searchRecord struct {
	path string
	line int
	text string
}

// Spend the existing preview budget across hits, rather than losing every
// middle hit behind one long source line. Full search output stays in Text
// for the UI and the existing saved-output mechanism.
func (s *SearchCode) previewSearchHits(result Result, preview *searchContext, query string) Result {
	if result.Err != nil || len(result.Text) <= core.ModelOutputInlineBytes || preview == nil || len(preview.records) == 0 || len(preview.records) > maxSearchPreviewHits {
		return result
	}
	notice := "[search preview: long matching lines shortened; full result saved]"
	if preview.limit > 0 {
		notice += "\n" + searchLimitNotice(preview.limit)
	}
	prefixes := make([]string, len(preview.records))
	bodies := make([]string, len(preview.records))
	order := make([]int, len(preview.records))
	remaining := core.ModelOutputPreviewBytes - len(notice)
	for i, hit := range preview.records {
		prefixes[i] = fmt.Sprintf("%s:%d:", s.displayPath(hit.path), hit.line)
		remaining -= len(prefixes[i]) + 1
		bodies[i], order[i] = hit.text, i
	}
	// Never shorten paths into ambiguous references or hide some of the hits.
	// If metadata alone crowds the budget, retain the existing generic preview.
	if remaining < len(bodies)*64 {
		return result
	}
	re, _ := regexp.Compile(query)
	sort.SliceStable(order, func(i, j int) bool { return len(bodies[order[i]]) < len(bodies[order[j]]) })
	for position, i := range order {
		share := remaining / (len(order) - position)
		bodies[i] = searchLineExcerpt(bodies[i], re, min(share, 512))
		remaining -= len(bodies[i])
	}
	var b strings.Builder
	for i := range prefixes {
		b.WriteString(prefixes[i])
		b.WriteString(bodies[i])
		b.WriteByte('\n')
	}
	b.WriteString(notice)
	result.ModelPreview = b.String()
	return result
}

func searchLineExcerpt(text string, re *regexp.Regexp, budget int) string {
	if len(text) <= budget {
		return text
	}
	// Both ellipses fit inside the allocation. Find the query on the original
	// line, so a match late in minified code remains visible.
	width := budget - 6
	start := 0
	if re != nil {
		if match := re.FindStringIndex(text); match != nil {
			start = max(0, match[0]-min(96, max(0, (width-(match[1]-match[0]))/2)))
		}
	}
	start = min(start, max(0, len(text)-width))
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	end := min(len(text), start+width)
	for end > start && end < len(text) && !utf8.RuneStart(text[end]) {
		end--
	}
	out := text[start:end]
	if start > 0 {
		out = "..." + out
	}
	if end < len(text) {
		out += "..."
	}
	return out
}
