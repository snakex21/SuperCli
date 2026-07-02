package files

import (
	"fmt"
	"strings"

	"supercli/internal/tools/fileops"
)

// Output bounds for the read_lines / read_context tools.
// The line-count is already capped upstream (MaxLineRange /
// MaxContextRadius), but a file can still have pathologically
// long lines (e.g. minified JSON on a single line), so we also
// bound the total bytes and each individual line. Every cap
// emits a marker that tells the model exactly how to get the
// rest — otherwise a truncated read provokes a retry loop.
const (
	// maxReadOutputBytes caps the assembled text of a single
	// read result (~16k tokens at chars/4).
	maxReadOutputBytes = 64 * 1024

	// maxReadLineChars caps a single rendered line so one giant
	// line can't dominate the budget or hide which line it was.
	maxReadLineChars = 2000
)

// renderLines formats numbered file lines with hard byte and
// per-line caps. When a line is longer than maxReadLineChars it
// is cut and marked; when the running total exceeds
// maxReadOutputBytes rendering stops and a trailing marker tells
// the model to narrow its range (or use ctx_execute for the raw
// bytes).
func renderLines(lines []fileops.LineRange) string {
	var b strings.Builder
	for i, l := range lines {
		content := l.Content
		lineTrunc := ""
		if len(content) > maxReadLineChars {
			over := len(content) - maxReadLineChars
			content = content[:maxReadLineChars]
			lineTrunc = fmt.Sprintf(" …[+%d chars on this line truncated; use ctx_execute for the full line]", over)
		}
		row := fmt.Sprintf("%4d | %s%s\n", l.Number, content, lineTrunc)
		if b.Len()+len(row) > maxReadOutputBytes && b.Len() > 0 {
			remaining := len(lines) - i
			fmt.Fprintf(&b, "... (output truncated at %d KB; %d more line(s) not shown — request line %d onward, or a smaller range)\n",
				maxReadOutputBytes/1024, remaining, l.Number)
			break
		}
		b.WriteString(row)
	}
	return b.String()
}
