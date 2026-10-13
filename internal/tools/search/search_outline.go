package search

import (
	"fmt"
	"strings"

	"supercli/internal/tools/core"
)

// A broad search needs a map of owners, not a head/tail dump that hides middle
// files. This is an explicitly partial preview; OutputStore retains the original.
// Explicit context and small targeted results keep their current representation.
func (s *SearchCode) outlineSearchHits(result Result, preview *searchContext, query string) Result {
	if result.Err != nil || len(result.Text) <= core.ModelOutputInlineBytes || preview == nil || len(preview.records) < 8 {
		return result
	}
	paths := make(map[string]bool)
	for _, record := range preview.records {
		paths[record.path] = true
	}
	if len(paths) < 4 {
		return result
	}
	var b strings.Builder
	b.WriteString("[Broad search overview; selected matching lines per file; full captured result saved]\n")
	shown := make(map[string]int)
	matcher := newSearchExcerptMatcher(query)
	footer := "Focus the owning files or symbols; output_mode=files locates matching paths. Use read_many for needed ranges; retrieve saved output only for omitted evidence."
	if preview.limit > 0 {
		footer += "\n" + searchLimitNotice(preview.limit)
	}
	// Show every captured owner once before spending spare bytes on second
	// matches. If complete path references cannot fit, keep the saved-output
	// fallback; never shorten a path or silently hide middle owners.
	for _, record := range preview.records {
		if shown[record.path] > 0 {
			continue
		}
		row := fmt.Sprintf("%s:%d:%s\n", s.displayPath(record.path), record.line, matcher.excerpt(record.text, 140))
		if b.Len()+len(row)+len(footer) > core.ModelOutputPreviewBytes {
			return result
		}
		b.WriteString(row)
		shown[record.path]++
	}
	visited := make(map[string]bool)
	for _, record := range preview.records {
		if !visited[record.path] {
			visited[record.path] = true
			continue
		}
		if shown[record.path] >= 2 {
			continue
		}
		row := fmt.Sprintf("%s:%d:%s\n", s.displayPath(record.path), record.line, matcher.excerpt(record.text, 140))
		if b.Len()+len(row)+len(footer) > core.ModelOutputPreviewBytes {
			break
		}
		b.WriteString(row)
		shown[record.path]++
	}
	b.WriteString(footer)
	if b.Len() <= core.ModelOutputPreviewBytes {
		result.ModelPreview = b.String()
	}
	return result
}
