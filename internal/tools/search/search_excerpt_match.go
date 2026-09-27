package search

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Match the same query semantics as the scanner: regular expressions first,
// otherwise literal substrings after strings.ToLower, not Unicode simple fold.
// Used only when an already captured long line needs a bounded excerpt.
type searchExcerptMatcher struct {
	pattern *regexp.Regexp
	literal string
}

func newSearchExcerptMatcher(query string) searchExcerptMatcher {
	re, err := regexp.Compile(query)
	if err == nil {
		return searchExcerptMatcher{pattern: re}
	}
	return searchExcerptMatcher{literal: strings.ToLower(query)}
}

func (m searchExcerptMatcher) find(text string) []int {
	if m.pattern != nil {
		return m.pattern.FindStringIndex(text)
	}
	if m.literal == "" {
		return nil
	}
	lower := strings.ToLower(text)
	start := strings.Index(lower, m.literal)
	if start < 0 {
		return nil
	}
	end := start + len(m.literal)
	if lower == text {
		return []int{start, end}
	}
	// Lowercasing can change UTF-8 byte lengths (for example İ -> i). Map
	// the matched lowercased span back to the untouched original source.
	lowerOffset, sourceStart := 0, -1
	for sourceOffset, r := range text {
		if lowerOffset == start {
			sourceStart = sourceOffset
		}
		if lowerOffset == end {
			return []int{sourceStart, sourceOffset}
		}
		if r < utf8.RuneSelf {
			lowerOffset++
		} else {
			lowerOffset += utf8.RuneLen(unicode.ToLower(r))
		}
	}
	if sourceStart >= 0 && lowerOffset == end {
		return []int{sourceStart, len(text)}
	}
	return nil
}

func (m searchExcerptMatcher) excerpt(text string, budget int) string {
	if len(text) <= budget {
		return text
	}
	return searchLineExcerptMatch(text, m.find(text), budget)
}
