package fileops

import (
	"strings"
)

// Anchor resilience for patch_file.
//
// patch_file used to be byte-exact and nothing else: if `old` was not in the
// file verbatim, the call failed. patch_diag.go already proved what the misses
// actually were — "the content was almost never wrong, the calls missed on
// whitespace or drifted in the tail" — and it spent tokens explaining that to
// the model so the model could send the same block again with different
// spacing. That is a whole round-trip bought with the tool's own diagnosis of
// the answer. When the tool can already prove where the text is, it should
// patch it.
//
// So matching is tiered, and every tier still has to land on EXACTLY
// expected_count matches or the call fails as before:
//
//	tier 0  exact bytes                       (unchanged, always tried first)
//	tier 1  line endings normalised           (LF `old` against a CRLF file)
//	tier 2  whole-line block, trailing         (trailing spaces, \r, and a
//	        whitespace and indentation shift    uniformly different indent)
//
// Relaxation is attempted ONLY when the exact count is 0. An `old` that occurs
// twice when one was expected is an ambiguity, not a spacing problem, and it
// keeps the old error with its line list — relaxing there could pick a
// different block than the one the model meant.
//
// Tier 2 matches whole lines only. A mid-line fragment either matches exactly
// (tier 0 got it) or does not match at all; it is never fuzzily spliced into
// the middle of a line.

// patchSpan is one located replacement: a byte range in the file and the text
// to put there.
type patchSpan struct {
	start int
	end   int
	text  string
}

// relaxedMatch locates `old` in content under progressively looser whitespace
// rules and returns exactly `want` non-overlapping spans plus the replacement
// text for each, adapted to how the file actually writes that text. mode names
// the tier that matched, for the success message; ok is false when no tier
// produced exactly `want` matches.
func relaxedMatch(content, old, new string, want int) (spans []patchSpan, mode string, ok bool) {
	if old == "" || want <= 0 || len(content) > diagMaxContent {
		return nil, "", false
	}
	if spans, ok := eolMatch(content, old, new, want); ok {
		return spans, "line endings normalised", true
	}
	if spans, note, ok := lineBlockMatch(content, old, new, want); ok {
		return spans, note, true
	}
	return nil, "", false
}

// eolMatch retries the exact match with `old` converted to the file's line
// endings. A model that read a CRLF file through read_lines gets LF-joined
// text back and sends LF text; on Windows that is the cheapest possible miss
// and the one with the least ambiguity about what was meant.
func eolMatch(content, old, new string, want int) ([]patchSpan, bool) {
	if !strings.Contains(old, "\n") {
		return nil, false
	}
	var oldAlt, newAlt string
	switch {
	case strings.Contains(content, "\r\n") && !strings.Contains(old, "\r\n"):
		oldAlt, newAlt = toCRLF(old), toCRLF(new)
	case strings.Contains(old, "\r\n") && !strings.Contains(content, "\r\n"):
		oldAlt, newAlt = stripCR(old), stripCR(new)
	default:
		return nil, false
	}
	if oldAlt == old || strings.Count(content, oldAlt) != want {
		return nil, false
	}
	spans := make([]patchSpan, 0, want)
	off := 0
	for len(spans) < want {
		i := strings.Index(content[off:], oldAlt)
		if i < 0 {
			break
		}
		at := off + i
		spans = append(spans, patchSpan{start: at, end: at + len(oldAlt), text: newAlt})
		off = at + len(oldAlt)
	}
	if len(spans) != want {
		return nil, false
	}
	return spans, true
}

// fileLine is one line of the file with its byte range (content only, EOL
// excluded) and the EOL that terminates it ("" at EOF without a newline).
type fileLine struct {
	start int
	end   int
	eol   string
}

// splitFileLines indexes content into lines without copying it.
func splitFileLines(content string) []fileLine {
	lines := make([]fileLine, 0, 1+strings.Count(content, "\n"))
	start := 0
	for start <= len(content) {
		nl := strings.IndexByte(content[start:], '\n')
		if nl < 0 {
			if start < len(content) {
				lines = append(lines, fileLine{start: start, end: len(content)})
			}
			break
		}
		end := start + nl
		eol := "\n"
		if end > start && content[end-1] == '\r' {
			end--
			eol = "\r\n"
		}
		lines = append(lines, fileLine{start: start, end: end, eol: eol})
		start = start + nl + 1
	}
	return lines
}

// lineBlockMatch matches `old` as a run of whole lines, ignoring trailing
// whitespace and a uniform difference in leading indentation. It is the tier
// that absorbs the ergonomics the deleted edit_line tool had: a model may send
// the bare line it wants changed without reproducing the tab in front of it.
//
// The returned note is empty when nothing but trailing whitespace differed and
// names the indentation shift when there was one, because a silently
// re-indented replacement is exactly the kind of surprise the model has no
// other way to see.
func lineBlockMatch(content, old, new string, want int) ([]patchSpan, string, bool) {
	oldBody, oldHadEOL := trimOneEOL(old)
	if strings.TrimSpace(oldBody) == "" {
		return nil, "", false
	}
	oldLines := splitLF(oldBody)
	oldKeys := make([]string, len(oldLines))
	for i, l := range oldLines {
		oldKeys[i] = rtrimSpace(l)
	}
	oldIndent := commonIndent(oldKeys)
	oldCore := make([]string, len(oldKeys))
	for i, k := range oldKeys {
		if k == "" {
			continue
		}
		oldCore[i] = strings.TrimPrefix(k, oldIndent)
	}

	lines := splitFileLines(content)
	fileEOL := dominantEOL(content)
	spans := make([]patchSpan, 0, want)
	reindented := false

	for s := 0; s+len(oldCore) <= len(lines); s++ {
		candIndent, ok := blockMatchesAt(content, lines, s, oldCore)
		if !ok {
			continue
		}
		body, shifted, ok := reindentNew(new, oldIndent, candIndent, blockEOL(lines, s, fileEOL))
		if !ok {
			continue
		}
		if shifted {
			reindented = true
		}
		last := lines[s+len(oldCore)-1]
		end := last.end
		if oldHadEOL {
			end += len(last.eol)
			if last.eol != "" {
				body += last.eol
			}
		}
		spans = append(spans, patchSpan{start: lines[s].start, end: end, text: body})
		if len(spans) > want {
			return nil, "", false
		}
		s += len(oldCore) - 1
	}
	if len(spans) != want {
		return nil, "", false
	}
	note := "whitespace normalised"
	if reindented {
		note = "whitespace normalised, replacement re-indented to match the file"
	}
	return spans, note, true
}

// blockMatchesAt reports whether the file lines starting at s carry oldCore
// under a single shared indent, and returns that indent.
func blockMatchesAt(content string, lines []fileLine, s int, oldCore []string) (string, bool) {
	indent := ""
	haveIndent := false
	for i, core := range oldCore {
		key := rtrimSpace(content[lines[s+i].start:lines[s+i].end])
		if core == "" {
			if key != "" {
				return "", false
			}
			continue
		}
		if !haveIndent {
			if !strings.HasSuffix(key, core) {
				return "", false
			}
			cand := key[:len(key)-len(core)]
			if !allSpace(cand) {
				return "", false
			}
			indent, haveIndent = cand, true
			continue
		}
		if key != indent+core {
			return "", false
		}
	}
	if !haveIndent {
		return "", false
	}
	return indent, true
}

// reindentNew rewrites the replacement text for a block whose real indentation
// is candIndent while the model wrote it under oldIndent. Relative indentation
// inside the replacement is preserved: the difference is applied as a prefix
// added to, or removed from, every non-blank line. When the two indents are
// neither a prefix of the other (tabs against spaces) the shift is refused —
// guessing there would silently mix indentation styles in the file.
func reindentNew(new, oldIndent, candIndent, eol string) (body string, shifted bool, ok bool) {
	newBody, _ := trimOneEOL(new)
	newLines := splitLF(newBody)
	for i := range newLines {
		newLines[i] = strings.TrimSuffix(newLines[i], "\r")
	}
	switch {
	case oldIndent == candIndent:
	case strings.HasPrefix(candIndent, oldIndent):
		extra := candIndent[len(oldIndent):]
		for i, l := range newLines {
			if strings.TrimSpace(l) == "" {
				continue
			}
			newLines[i] = extra + l
		}
		shifted = true
	case strings.HasPrefix(oldIndent, candIndent):
		drop := oldIndent[len(candIndent):]
		for i, l := range newLines {
			if strings.TrimSpace(l) == "" {
				continue
			}
			if !strings.HasPrefix(l, drop) {
				return "", false, false
			}
			newLines[i] = strings.TrimPrefix(l, drop)
		}
		shifted = true
	default:
		return "", false, false
	}
	return strings.Join(newLines, eol), shifted, true
}

// blockEOL is the line ending the matched block already uses, so a
// multi-line replacement is written the way the rest of the file is.
func blockEOL(lines []fileLine, s int, fallback string) string {
	if lines[s].eol != "" {
		return lines[s].eol
	}
	return fallback
}

// dominantEOL reports whether the file is CRLF or LF, defaulting to the host's
// least surprising choice for a file with no newline at all.
func dominantEOL(content string) string {
	crlf := strings.Count(content, "\r\n")
	if crlf > 0 && crlf*2 >= strings.Count(content, "\n") {
		return "\r\n"
	}
	return "\n"
}

// splice applies non-overlapping spans, which must be sorted by start.
func splice(content string, spans []patchSpan) string {
	var b strings.Builder
	b.Grow(len(content))
	off := 0
	for _, sp := range spans {
		b.WriteString(content[off:sp.start])
		b.WriteString(sp.text)
		off = sp.end
	}
	b.WriteString(content[off:])
	return b.String()
}

func trimOneEOL(s string) (string, bool) {
	if strings.HasSuffix(s, "\r\n") {
		return s[:len(s)-2], true
	}
	if strings.HasSuffix(s, "\n") {
		return s[:len(s)-1], true
	}
	return s, false
}

func splitLF(s string) []string {
	parts := strings.Split(s, "\n")
	for i := range parts {
		parts[i] = strings.TrimSuffix(parts[i], "\r")
	}
	return parts
}

func toCRLF(s string) string { return strings.ReplaceAll(stripCR(s), "\n", "\r\n") }

func stripCR(s string) string { return strings.ReplaceAll(s, "\r", "") }

func rtrimSpace(s string) string {
	end := len(s)
	for end > 0 && isSpaceByte(s[end-1]) {
		end--
	}
	return s[:end]
}

func allSpace(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isSpaceByte(s[i]) {
			return false
		}
	}
	return true
}

// commonIndent is the longest leading-whitespace run shared by every non-blank
// line of the block.
func commonIndent(keys []string) string {
	indent := ""
	first := true
	for _, k := range keys {
		if k == "" {
			continue
		}
		lead := k[:len(k)-len(strings.TrimLeft(k, " \t"))]
		if first {
			indent, first = lead, false
			continue
		}
		indent = commonPrefix(indent, lead)
		if indent == "" {
			return ""
		}
	}
	return indent
}

func appendUnique(list []string, s string) []string {
	if s == "" {
		return list
	}
	for _, e := range list {
		if e == s {
			return list
		}
	}
	return append(list, s)
}

func commonPrefix(a, b string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return a[:i]
}
