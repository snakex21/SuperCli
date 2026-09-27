package fileops

import (
	"fmt"
	"strings"
)

// Show already-read source only for a unique normalized match. The count is
// evidence about whitespace, never permission to apply an inexact patch.
func whitespaceMatchEvidence(content, old, normalized, needle string, collapse bool, count int) string {
	if count != 1 {
		return ""
	}
	at := strings.Index(normalized, needle)
	if at < 0 {
		return ""
	}
	start, end := whitespaceSourceSpan(content, at, at+len(needle), collapse)
	if start < 0 || end <= start {
		return ""
	}
	// Include real indentation when the match begins a line. A small complete
	// span can also expose reflowed newlines, with tabs/newlines quoted visibly.
	displayStart := start
	lineStart := strings.LastIndexByte(content[:start], '\n') + 1
	if strings.Trim(content[lineStart:start], " \t\r") == "" {
		displayStart = lineStart
	}
	if end-displayStart <= diagSnippet {
		return fmt.Sprintf("; file text at line %d: %q", lineOf(content, displayStart), content[displayStart:end])
	}
	// A large block needs the first differing line, not an unrelated prefix.
	trimmedOld := strings.Trim(old, " \t\r\n\v\f")
	actual := content[start:end]
	equal := 0
	for equal < len(trimmedOld) && equal < len(actual) && trimmedOld[equal] == actual[equal] {
		equal++
	}
	if equal == len(trimmedOld) && equal == len(actual) {
		equal = 0
	}
	off := start + equal
	return fmt.Sprintf("; file line %d reads: %q", lineOf(content, off), lineSnippetAt(content, off))
}

// Map a normalized match back to original byte offsets without allocating a
// per-byte index for a potentially 4 MiB file. Both endpoints are non-space
// bytes because the diagnostic needles are trimmed by their normalization.
func whitespaceSourceSpan(content string, from, to int, collapse bool) (int, int) {
	start, emitted := -1, 0
	pendingSpace := false
	for i := 0; i < len(content); i++ {
		if isSpaceByte(content[i]) {
			pendingSpace = true
			continue
		}
		if collapse && pendingSpace && emitted > 0 {
			emitted++
		}
		pendingSpace = false
		if emitted == from {
			start = i
		}
		emitted++
		if emitted == to {
			return start, i + 1
		}
	}
	return -1, -1
}
