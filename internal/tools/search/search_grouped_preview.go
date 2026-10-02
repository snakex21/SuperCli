package search

import (
	"strconv"
	"strings"

	"supercli/internal/tools/core"
)

// Keep every captured row and its order. Only the model copy groups repeated
// paths; Text and existing saved output retain file:line:content.
func (s *SearchCode) groupedSearchPreview(result Result, preview *searchContext) string {
	if len(preview.records) < 4 || len(result.Text) < 1024 {
		return ""
	}
	const notice = "[search hits grouped by file; numbers are source line numbers]\n"
	var b strings.Builder
	b.WriteString(notice)
	previous := ""
	for i, hit := range preview.records {
		if i == 0 || hit.path != previous {
			path := strconv.Quote(s.displayPath(hit.path))
			if b.Len()+len(path)+7 > core.ModelOutputPreviewBytes {
				return ""
			}
			b.WriteString("== ")
			b.WriteString(path)
			b.WriteString(" ==\n")
			previous = hit.path
		}
		number := strconv.Itoa(hit.line)
		if b.Len()+len(number)+len(hit.text)+4 > core.ModelOutputPreviewBytes {
			return ""
		}
		b.WriteString(number)
		b.WriteString(" | ")
		b.WriteString(hit.text)
		b.WriteByte('\n')
	}
	if preview.limit > 0 {
		b.WriteString(searchLimitNotice(preview.limit))
	} else {
		// Match the compact location result's final newline policy.
		return selectiveSearchPreview(strings.TrimSuffix(b.String(), "\n"), len(result.Text))
	}
	return selectiveSearchPreview(b.String(), len(result.Text))
}

func selectiveSearchPreview(preview string, originalBytes int) string {
	// Small savings keep the inline form. The existing long-line preview
	// remains the fallback whenever complete grouping does not fit.
	if len(preview) > core.ModelOutputPreviewBytes ||
		originalBytes-len(preview) < 512 ||
		len(preview)*4 > originalBytes*3 {
		return ""
	}
	return preview
}
