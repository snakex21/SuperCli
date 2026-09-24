package search

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"supercli/internal/tools/fileops"
)

const (
	maxSearchContextRadius = 20
	maxSearchContextLines  = 500
	maxSearchContextBytes  = 2048 // per line, matching bounded file reads
)

type searchHit struct {
	path string
	line int
}

type searchContext struct {
	radius    int
	hits      []searchHit
	records   []searchRecord
	include   *searchGlob
	limit     int
	query     string
	longLines map[searchHit]string
}

func captureSearchHit(previews []*searchContext, path string, line int, text string) {
	if len(previews) > 0 && previews[0] != nil && len(previews[0].records) <= maxSearchPreviewHits {
		previews[0].records = append(previews[0].records, searchRecord{path: path, line: line, text: text})
	}
	if len(previews) > 0 && previews[0] != nil && previews[0].radius > 0 {
		p := previews[0]
		hit := searchHit{path: path, line: line}
		// At most 500 matching lines can enter the bounded context view. Keep
		// their already captured text when a head-only reread would cut it.
		if len(text) > maxSearchContextBytes && len(p.hits) < maxSearchContextLines {
			if p.longLines == nil {
				p.longLines = make(map[searchHit]string)
			}
			p.longLines[hit] = text
		}
		p.hits = append(p.hits, hit)
	}
}

// With context enabled rg separates the filename with NUL, so filenames with
// colons cannot be mistaken for line numbers or interpreted as another path.
func parseContextSearchHit(line string) (path string, number int, text string, ok bool) {
	end := strings.IndexByte(line, 0)
	if end < 0 {
		return "", 0, "", false
	}
	rest := line[end+1:]
	colon := strings.IndexByte(rest, ':')
	if colon < 1 {
		return "", 0, "", false
	}
	number, err := strconv.Atoi(rest[:colon])
	if err != nil || number < 1 {
		return "", 0, "", false
	}
	return line[:end], number, rest[colon+1:], true
}

type searchWindow struct{ from, to int }

// renderSearchContext reads only hit neighborhoods. Overlaps are merged, so a
// cluster of matches does not duplicate code. Explicit context=0 and broad
// automatic searches never reach this path or pay for additional file reads.
func (s *SearchCode) renderSearchContext(ctx context.Context, preview *searchContext, locations Result) Result {
	if preview == nil || len(preview.hits) == 0 {
		return locations
	}
	var matchPattern *regexp.Regexp
	if len(preview.longLines) > 0 {
		matchPattern, _ = regexp.Compile(preview.query)
		if matchPattern == nil {
			// Match the fallback scanner's handling of an invalid regexp.
			matchPattern = regexp.MustCompile("(?i)" + regexp.QuoteMeta(preview.query))
		}
	}
	var retained string
	var paths []string
	grouped := make(map[string][]int)
	for _, hit := range preview.hits {
		if _, ok := grouped[hit.path]; !ok {
			paths = append(paths, hit.path)
		}
		grouped[hit.path] = append(grouped[hit.path], hit.line)
	}
	var b strings.Builder
	printed := 0
	for _, path := range paths {
		numbers := grouped[path]
		sort.Ints(numbers)
		matches := make(map[int]bool, len(numbers))
		var windows []searchWindow
		for _, line := range numbers {
			matches[line] = true
			from, to := max(1, line-preview.radius), line+preview.radius
			last := len(windows) - 1
			if last >= 0 && from <= windows[last].to+1 && to-windows[last].from < maxSearchContextLines {
				windows[last].to = max(windows[last].to, to)
			} else {
				windows = append(windows, searchWindow{from: from, to: to})
			}
		}
		// Reserve this file's output budget before reading; only selected lines
		// are retained, even when matches are far apart in a large file.
		spans := make([]fileops.LineSpan, 0, len(windows))
		planned := printed
		for _, window := range windows {
			width := window.to - window.from + 1
			if planned+width > maxSearchContextLines {
				break
			}
			planned += width
			spans = append(spans, fileops.LineSpan{From: window.from, To: window.to})
		}
		reads := fileops.ReadRangesBounded(ctx, path, spans, maxSearchContextBytes)
		for i, window := range windows {
			if err := ctx.Err(); err != nil {
				return Result{Text: b.String(), Err: err}
			}
			if i >= len(reads) {
				b.WriteString("[search context capped at 500 lines; narrow query/path or use context=0 for locations]\n")
				return Result{Text: b.String(), RetainedText: retained}
			}
			lines, err := reads[i].Lines, reads[i].Err
			if err != nil {
				// Preserve locations when a file changes/disappears between the
				// search and context read; never turn that into "no matches".
				return Result{Text: locations.Text, Err: fmt.Errorf("search context unavailable: %w", err)}
			}
			end := window.to
			if len(lines) > 0 {
				end = lines[len(lines)-1].Number
			}
			fmt.Fprintf(&b, "== %s:%d-%d ==\n", s.displayPath(path), window.from, end)
			for _, line := range lines {
				marker := " "
				if matches[line.Number] {
					marker = ">"
				}
				content := strings.TrimSuffix(line.Content, "\r")
				if captured, ok := preview.longLines[searchHit{path: path, line: line.Number}]; ok {
					// Leave an already visible match byte-identical. Only repair a
					// match that the bounded head would actually cut or omit.
					if match := matchPattern.FindStringIndex(captured); match != nil && match[1] > maxSearchContextBytes {
						content = searchLineExcerpt(strings.TrimSuffix(captured, "\r"), matchPattern, maxSearchContextBytes)
						retained = locations.Text
					}
				}
				fmt.Fprintf(&b, "%s %4d | %s\n", marker, line.Number, content)
			}
			printed += len(lines)
		}
	}
	text := strings.TrimSuffix(b.String(), "\n")
	result := Result{Text: text, RetainedText: retained}
	if preview.limit > 0 {
		result.Text = searchLimitedResult(text, preview.limit, nil).Text
	}
	return result
}
