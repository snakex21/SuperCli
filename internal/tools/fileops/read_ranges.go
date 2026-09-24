package fileops

import (
	"context"
	"fmt"
	"os"
	"slices"
	"sort"
)

// LineSpan is an inclusive, 1-based range. Callers bound the number of spans;
// every span also obeys the same MaxLineRange limit as ReadLinesBounded.
type LineSpan struct{ From, To int }

type LineReadResult struct {
	Lines []LineRange
	Err   error
}

// ReadRangesBounded reads one file once, retaining only the union of requested
// lines. Results preserve request order and independent validation/EOF errors.
// There is no state shared between calls, so subsequent calls see file edits.
func ReadRangesBounded(ctx context.Context, path string, spans []LineSpan, maxLineBytes int) []LineReadResult {
	results := make([]LineReadResult, len(spans))
	if len(spans) == 1 {
		results[0].Lines, results[0].Err = ReadLinesBounded(ctx, path, spans[0].From, spans[0].To, maxLineBytes)
		return results
	}
	windows := make([]LineSpan, 0, len(spans))
	for i, span := range spans {
		results[i].Err = validateLineRange(span.From, span.To)
		if results[i].Err == nil {
			windows = append(windows, span)
		}
	}
	if len(windows) == 0 {
		return results
	}
	slices.SortFunc(windows, func(a, b LineSpan) int {
		if a.From < b.From {
			return -1
		}
		if a.From > b.From {
			return 1
		}
		return 0
	})
	merged := windows[:0]
	for _, span := range windows {
		last := len(merged) - 1
		if last >= 0 && (span.From <= merged[last].To || span.From-merged[last].To == 1) {
			merged[last].To = max(merged[last].To, span.To)
		} else {
			merged = append(merged, span)
		}
	}
	lines, completed, readErr := readRanges(ctx, path, merged, maxLineBytes)
	for i, span := range spans {
		if results[i].Err != nil {
			continue
		}
		switch {
		case readErr != nil && completed < span.To:
			results[i].Err = readErr
		case span.From > completed:
			results[i].Err = fmt.Errorf("fileops.ReadLines: from=%d exceeds file length %d", span.From, completed)
		default:
			start := sort.Search(len(lines), func(j int) bool { return lines[j].Number >= span.From })
			end := sort.Search(len(lines), func(j int) bool { return lines[j].Number > span.To })
			// Independent slices allow callers to normalize CRLF without changing
			// overlapping results. Line strings themselves remain shared.
			results[i].Lines = slices.Clone(lines[start:end])
		}
	}
	return results
}

func readRanges(ctx context.Context, path string, windows []LineSpan, maxLineBytes int) ([]LineRange, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, FileErr(err, path)
	}
	defer f.Close()
	lines, completed, err := readLineWindows(ctx, f, path, windows[0].From, windows[len(windows)-1].To, maxLineBytes, windows)
	if err != nil {
		err = FileErr(err, path)
	}
	return lines, completed, err
}
