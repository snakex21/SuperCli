package search

import "strings"

// historyQueryTerms quotes punctuation-bearing bare terms, e.g. retry.go or
// C:\src\retry.go. FTS operators and already quoted strings are left in place.
// This is one local pass before the existing query, not a retry or relaxation.
func historyQueryTerms(query string) string {
	var out strings.Builder
	copied := 0
	for i := 0; i < len(query); {
		if query[i] == '"' {
			i++
			closed := false
			for i < len(query) {
				if query[i] != '"' {
					i++
				} else if i+1 < len(query) && query[i+1] == '"' {
					i += 2
				} else {
					i++
					closed = true
					break
				}
			}
			if !closed {
				return query // keep malformed explicit syntax as an error
			}
			continue
		}
		if historyQueryBoundary(query[i]) || query[i] == '-' {
			i++ // leading '-' is FTS column exclusion, not a filename repair
			continue
		}
		start, quote := i, false
		for i < len(query) {
			if historyQueryBoundary(query[i]) {
				// A Windows drive colon belongs to the path, not a column filter.
				if query[i] != ':' || i != start+1 || !historyDriveLetter(query[start]) ||
					i+1 >= len(query) || (query[i+1] != '/' && query[i+1] != '\\') {
					break
				}
			}
			c := query[i]
			if !(c >= 128 || c == 26 || c == '_' || c >= '0' && c <= '9' || historyDriveLetter(c)) {
				quote = true
			}
			i++
		}
		if !quote {
			continue
		}
		out.WriteString(query[copied:start])
		// Avoid merging adjacent quoted strings into one escaped-quote string.
		if start > 0 && query[start-1] == '"' {
			out.WriteByte(' ')
		}
		out.WriteByte('"')
		out.WriteString(query[start:i])
		out.WriteByte('"')
		if i < len(query) && query[i] == '"' {
			out.WriteByte(' ')
		}
		copied = i
	}
	if copied == 0 {
		return query
	}
	out.WriteString(query[copied:])
	return out.String()
}

func historyQueryBoundary(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\f', '\v', '"', '(', ')', '{', '}', ':', '+', '*', '^', ',':
		return true
	}
	return false
}

func historyDriveLetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}
