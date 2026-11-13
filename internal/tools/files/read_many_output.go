package files

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"supercli/internal/tools/core"
	"supercli/internal/tools/fileops"
)

const (
	// readManyNoteReserve is kept free for the continuation note whenever
	// the batch does not fit; the note names up to one follow-up batch.
	readManyNoteReserve = 1536
	// readManyErrorBytes bounds one failed item's diagnostic.
	readManyErrorBytes = 1024
)

// renderReadMany packs whole ranges, in request order, into budget bytes.
// A range that does not fit is never trimmed in the middle: it is left out
// and named, with a ready follow-up call, in a note before the summary. Only
// when not even the first range fits is it cut, at a top-level declaration
// boundary where possible, and its remaining lines are named the same way.
// Failed items are always reported. The last line stays the summary that
// pruning and fact extraction parse; it counts only returned sections.
func renderReadMany(outcomes []readManyOutcome, overflow []readManyRequest, budget int) Result {
	type section struct {
		file       string
		from, to   int
		body       string
		failed     bool
		include    bool
		partialEnd int // first line not returned when a range was cut
	}
	sections := make([]section, len(outcomes))
	total := 0
	for i, outcome := range outcomes {
		s := section{file: outcome.request.File, from: outcome.request.From, to: outcome.request.To}
		if outcome.err != nil {
			s.failed = true
			s.body = "error: " + boundedError(outcome.err.Error()) + "\n"
		} else {
			s.body = renderReadManyBody(outcome, len(outcome.lines))
		}
		sections[i] = s
		total += len(readManyHeader(0, s.file, s.from, s.to)) + len(s.body)
	}
	summaryReserve := len("[read_many: 12 ok, 12 failed]")
	avail := budget - summaryReserve
	fitsAll := total <= avail && len(overflow) == 0
	if !fitsAll {
		// Reserve what the note would take if every range were left out
		// (it names at most one follow-up batch), not a fixed block: on a
		// small shared budget a fixed reserve left room for four lines.
		all := make([]readManyRequest, 0, len(outcomes)+len(overflow))
		for _, outcome := range outcomes {
			all = append(all, wholeRequest(outcome))
		}
		all = append(all, overflow...)
		avail -= min(len(readManyContinuation(all, budget)), readManyNoteReserve)
	}
	used := 0
	anyIncluded := false
	var pending []readManyRequest
	for i := range sections {
		s := &sections[i]
		size := len(readManyHeader(0, s.file, s.from, s.to)) + len(s.body)
		switch {
		case s.failed:
			// Diagnostics are small and must reach the model.
			s.include = true
			used += size
		case fitsAll || used+size <= avail:
			s.include, anyIncluded = true, true
			used += size
		case !anyIncluded:
			// Nothing returned yet: cut this range at a line boundary so the
			// call still makes progress, and name the rest.
			outcome := outcomes[i]
			room := avail - used - len(readManyHeader(0, s.file, s.from, s.to)) - 160
			keep := linesThatFit(outcome.lines, room)
			keep = declarationCut(outcome.lines, keep)
			if keep == 0 {
				pending = append(pending, wholeRequest(outcome))
				continue
			}
			last := outcome.lines[keep-1].Number
			s.body = renderReadManyBody(readManyOutcome{lines: outcome.lines[:keep]}, keep)
			s.to = last
			s.partialEnd = last + 1
			rest := readManyRequest{File: s.file, From: last + 1, To: wholeRequest(outcome).To}
			s.body += fmt.Sprintf("[lines %d-%d not returned (output budget); file continues]\n", rest.From, rest.To)
			pending = append(pending, rest)
			s.include, anyIncluded = true, true
			used += len(readManyHeader(0, s.file, s.from, s.to)) + len(s.body)
		default:
			pending = append(pending, wholeRequest(outcomes[i]))
		}
	}
	pending = append(pending, overflow...)

	var b strings.Builder
	b.Grow(min(used+readManyNoteReserve+summaryReserve, budget+readManyNoteReserve))
	okCount, failedCount, n := 0, 0, 0
	for _, s := range sections {
		if !s.include {
			continue
		}
		n++
		b.WriteString(readManyHeader(n, s.file, s.from, s.to))
		b.WriteString(s.body)
		if s.failed {
			failedCount++
		} else {
			okCount++
		}
	}
	if len(pending) > 0 {
		b.WriteString(readManyContinuation(pending, budget))
	}
	summary := fmt.Sprintf("[read_many: %d ok, %d failed]", okCount, failedCount)
	b.WriteString(summary)
	result := Result{Text: b.String()}
	if okCount == 0 && failedCount > 0 && len(pending) == 0 {
		detail := result.Text
		if len(detail) > core.ModelOutputPreviewBytes {
			// Twelve long diagnostics must not become a generic error tail:
			// keep every item within the preview and retain the full text.
			result.RetainedText = detail
			detail = compactFailures(outcomes, summary)
		}
		causes := make([]error, 0, failedCount)
		for _, outcome := range outcomes {
			if outcome.err != nil {
				causes = append(causes, outcome.err)
			}
		}
		result.Err = core.SelfContainedErr(readManyBatchError{detail: detail, causes: causes})
	}
	return result
}

// wholeRequest is the range the caller asked for, before the line cap.
func wholeRequest(outcome readManyOutcome) readManyRequest {
	request := outcome.request
	if outcome.capped > request.To {
		request.To = outcome.capped
	}
	return request
}

// compactFailures shares the preview budget among failed items so each one
// keeps its path and the start of its diagnostic.
func compactFailures(outcomes []readManyOutcome, summary string) string {
	headers := make([]string, len(outcomes))
	room := core.ModelOutputPreviewBytes - len(summary)
	for i, outcome := range outcomes {
		headers[i] = fmt.Sprintf("== [%d] %s:%d-%d ==\n", i+1, previewFileName(outcome.request.File), outcome.request.From, outcome.request.To)
		room -= len(headers[i]) + len("error: ...\n")
	}
	share := max(0, room/max(1, len(outcomes)))
	var b strings.Builder
	for i, outcome := range outcomes {
		b.WriteString(headers[i])
		text := outcome.err.Error()
		if len(text) > share {
			cut := share
			for cut > 0 && !utf8.RuneStart(text[cut]) {
				cut--
			}
			text = text[:cut] + "..."
		}
		b.WriteString("error: " + text + "\n")
	}
	b.WriteString(summary)
	return b.String()
}

func readManyHeader(n int, file string, from, to int) string {
	if n == 0 {
		n = 12 // size estimate: widest index
	}
	return fmt.Sprintf("== [%d] %s:%d-%d ==\n", n, file, from, to)
}

// renderReadManyBody renders the first keep lines of a successful range,
// including its end-of-file and range-cap markers when the range is whole.
func renderReadManyBody(outcome readManyOutcome, keep int) string {
	var b strings.Builder
	for _, line := range outcome.lines[:keep] {
		b.WriteString(renderLineRow(line))
	}
	if keep == len(outcome.lines) {
		if outcome.eof && keep > 0 {
			fmt.Fprintf(&b, "[end of file at line %d]\n", outcome.lines[keep-1].Number)
		}
		if outcome.capped > 0 && !outcome.eof {
			fmt.Fprintf(&b, "[range capped at %d lines; requested lines %d-%d not read]\n",
				maxReadManyRange, outcome.request.To+1, outcome.capped)
		}
	}
	return b.String()
}

// linesThatFit returns how many leading lines render within room bytes.
func linesThatFit(lines []fileops.LineRange, room int) int {
	used := 0
	for i, line := range lines {
		used += len(renderLineRow(line))
		if used > room {
			return i
		}
	}
	return len(lines)
}

// declarationCut moves a cut of keep lines back to the start of a top-level
// declaration (a column-0 line after a blank or closing line), or failing
// that a blank line, so a function body is not split. It never gives up more
// than half of what fits.
func declarationCut(lines []fileops.LineRange, keep int) int {
	if keep <= 1 || keep >= len(lines) {
		return keep
	}
	for j := keep; j > keep/2; j-- {
		next, prev := lines[j].Content, lines[j-1].Content
		trimmed := strings.TrimSpace(prev)
		if startsTopLevel(next) && (trimmed == "" || prev == "}" || prev == ")" || strings.HasPrefix(prev, "}")) {
			return j
		}
	}
	for j := keep; j > keep/2; j-- {
		if strings.TrimSpace(lines[j-1].Content) == "" {
			return j
		}
	}
	return keep
}

func startsTopLevel(line string) bool {
	if line == "" {
		return false
	}
	switch line[0] {
	case ' ', '\t', '}', ')', ']':
		return false
	}
	return true
}

// readManyContinuation names every range that was not returned and gives one
// ready follow-up call of at most maxReadManyRequests ranges.
func readManyContinuation(pending []readManyRequest, budget int) string {
	next := pending[:min(len(pending), maxReadManyRequests)]
	parts := make([]string, len(next))
	for i, r := range next {
		parts[i] = fmt.Sprintf("%s:%d-%d", r.File, r.From, r.To)
	}
	more := ""
	if len(pending) > len(next) {
		more = fmt.Sprintf(" (then %d more)", len(pending)-len(next))
	}
	return fmt.Sprintf("[incomplete: %d range(s) not returned to stay within the %d KB output budget; nothing shown above was cut. Do not assume their content. Write down what you need from the files above before reading on (older results may be pruned). Next: read_many reads=%q%s]\n",
		len(pending), budget/1024, strings.Join(parts, " | "), more)
}

func boundedError(text string) string {
	if len(text) <= readManyErrorBytes {
		return text
	}
	cut := readManyErrorBytes
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "..."
}

// Error text stays bounded while errors.Is can still recognize cancellation or
// the individual I/O causes; converting diagnostics to one string would lose them.
type readManyBatchError struct {
	detail string
	causes []error
}

func (e readManyBatchError) Error() string   { return e.detail }
func (e readManyBatchError) Unwrap() []error { return e.causes }

func previewFileName(path string) string {
	const limit = 96
	if len(path) <= limit {
		return path
	}
	head, tail := 32, len(path)-(limit-32-3)
	for head > 0 && !utf8.RuneStart(path[head]) {
		head--
	}
	for tail < len(path) && !utf8.RuneStart(path[tail]) {
		tail++
	}
	return path[:head] + "..." + path[tail:]
}
