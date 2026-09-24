package core

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

const outputSearchContext = 160
const outputSearchExcerpts = 8

// search returns bounded excerpts from the saved result, not another execution.
// Literal matching works for paths, compiler messages and JSON without regex
// escaping. Byte offsets address the original text and remain usable by read.
func (s *OutputStore) search(ctx context.Context, args readOutputArgs) (string, error) {
	if s == nil {
		return "", fmt.Errorf("output store unavailable")
	}
	if args.Query == "" || utf8.RuneCountInString(args.Query) > 512 {
		return "", fmt.Errorf("query must contain 1 to 512 characters")
	}
	text, err := s.load(ctx, args.Handle)
	if err != nil {
		return "", err
	}
	if args.Offset < 0 || args.Offset > len(text) {
		return "", fmt.Errorf("offset %d outside output size %d", args.Offset, len(text))
	}
	budget := args.Limit
	if budget <= 0 {
		budget = outputReadDefault
	}
	if budget > outputReadMax {
		budget = outputReadMax
	}
	if budget < len(args.Query) {
		return "", fmt.Errorf("limit must fit the query (%d bytes)", len(args.Query))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[stored output %s search %q from byte %d of %d]\n", args.Handle, args.Query, args.Offset, len(text))
	cursor, coveredEnd, excerpts := args.Offset, 0, 0
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		// Matches fully inside the previous excerpt are already visible. Still
		// include one that crosses its end, so pagination cannot lose a match.
		cursor = max(cursor, coveredEnd-len(args.Query)+1)
		pos := strings.Index(text[cursor:], args.Query)
		if pos < 0 {
			if excerpts == 0 {
				b.WriteString("[no matches]")
			} else {
				b.WriteString("[no further matches]")
			}
			return b.String(), nil
		}
		pos += cursor
		if excerpts == outputSearchExcerpts || budget < len(args.Query) {
			fmt.Fprintf(&b, "[next search offset: %d]", pos)
			return b.String(), nil
		}
		// Fit the complete match before spending space on nearby context. This
		// handles minified JSON and multi-byte text without huge-line allocations.
		room := min(budget, len(args.Query)+2*outputSearchContext)
		start, end := outputSearchWindow(text, pos, len(args.Query), room, coveredEnd)
		fmt.Fprintf(&b, "[bytes %d:%d]\n%s\n", start, end, text[start:end])
		budget -= end - start
		excerpts++
		coveredEnd = end
		cursor = pos + len(args.Query)
	}
}

// Keep a complete matching line when it fits the same excerpt budget. Fixed
// centering can spend half the window on unrelated preceding lines and hide a
// value at the end of the matching line. All boundary scans are room-bounded;
// oversized/minified lines keep the byte-window fallback and saved offsets.
func outputSearchWindow(text string, pos, matchBytes, room, coveredEnd int) (int, int) {
	before := min(outputSearchContext, (room-matchBytes)/2)
	floor := min(coveredEnd, pos)
	start := max(0, pos-before, floor)
	end := min(len(text), start+room)

	lower := max(0, pos+matchBytes-room, floor)
	lineStart := lower
	haveStart := lower == 0 || text[lower-1] == '\n'
	if at := strings.LastIndexByte(text[lower:pos], '\n'); at >= 0 {
		lineStart = lower + at + 1
		haveStart = true
	}
	if haveStart {
		upper := min(len(text), lineStart+room)
		lineEnd := upper
		haveEnd := upper == len(text)
		if at := strings.IndexByte(text[pos+matchBytes:upper], '\n'); at >= 0 {
			lineEnd = pos + matchBytes + at + 1
			haveEnd = true
		}
		if haveEnd {
			start = min(max(start, lineEnd-room), lineStart)
			end = min(len(text), start+room)
		}
	}
	// Discard cut neighbor lines without dropping the match itself. This does
	// not spend the saved bytes on extra context or change the overall limit.
	if start > 0 && text[start-1] != '\n' {
		if at := strings.IndexByte(text[start:pos], '\n'); at >= 0 {
			start += at + 1
		}
	}
	if end < len(text) && text[end-1] != '\n' {
		if at := strings.LastIndexByte(text[pos+matchBytes:end], '\n'); at >= 0 {
			end = pos + matchBytes + at + 1
		}
	}
	return runeStartForward(text, start), runeStartBackward(text, end)
}
