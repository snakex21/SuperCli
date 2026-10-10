package tui

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const transcriptSearchStreamQueryBytes = 64

type transcriptSearchPattern struct {
	query     string
	failure   []int
	useStdlib bool
}

// Compile once per cold search, after the query has been trimmed/lowercased.
// Long or strongly overlapping queries retain the original stdlib path: the
// byte-at-a-time matcher can be slower on those inputs. No message is scanned
// to select a path, and the pattern is not retained by the result cache.
func newTranscriptSearchPattern(query string) transcriptSearchPattern {
	p := transcriptSearchPattern{query: query}
	if len(query) > transcriptSearchStreamQueryBytes {
		p.useStdlib = true
		return p
	}
	p.failure = make([]int, len(query))
	maxPrefix := 0
	for i, prefix := 1, 0; i < len(query); i++ {
		for prefix > 0 && query[i] != query[prefix] {
			prefix = p.failure[prefix-1]
		}
		if query[i] == query[prefix] {
			prefix++
		}
		p.failure[i] = prefix
		if prefix > maxPrefix {
			maxPrefix = prefix
		}
	}
	p.useStdlib = maxPrefix >= 8 && maxPrefix*2 >= len(query)
	return p
}

type transcriptSearchMatchState struct {
	query   string
	failure []int
	prefix  int
}

func (s *transcriptSearchMatchState) advance(c byte) bool {
	for s.prefix > 0 && c != s.query[s.prefix] {
		s.prefix = s.failure[s.prefix-1]
	}
	if c == s.query[s.prefix] {
		s.prefix++
	}
	return s.prefix == len(s.query)
}

// Match the complete Fields+Join+ToLower byte stream without constructing it.
// unicode.ToLower uses the same simple case mapping as strings.ToLower; invalid
// UTF-8 becomes RuneError there too. Query internal whitespace is unchanged.
func (p transcriptSearchPattern) containsNormalized(text string) bool {
	if p.query == "" {
		return true
	}
	s := transcriptSearchMatchState{query: p.query, failure: p.failure}
	haveText, pendingSpace := false, false
	var encoded [utf8.UTFMax]byte
	for offset := 0; offset < len(text); {
		r, width := rune(text[offset]), 1
		if r >= utf8.RuneSelf {
			r, width = utf8.DecodeRuneInString(text[offset:])
		}
		offset += width
		if transcriptSearchIsSpace(r) {
			pendingSpace = haveText
			continue
		}
		if pendingSpace {
			if s.advance(' ') {
				return true
			}
			pendingSpace = false
		}
		haveText = true
		if 'A' <= r && r <= 'Z' {
			r += 'a' - 'A'
		} else if r >= utf8.RuneSelf {
			r = unicode.ToLower(r)
		}
		if r < utf8.RuneSelf {
			if s.advance(byte(r)) {
				return true
			}
			continue
		}
		n := utf8.EncodeRune(encoded[:], r)
		for _, c := range encoded[:n] {
			if s.advance(c) {
				return true
			}
		}
	}
	return false
}

func transcriptSearchIsSpace(r rune) bool {
	if r < utf8.RuneSelf {
		return r == ' ' || ('\t' <= r && r <= '\r')
	}
	return unicode.IsSpace(r)
}

// Copy only a matching message's bounded, whitespace-normalized preview.
// Case and raw field bytes are preserved, including malformed UTF-8. Each
// malformed byte counts as one rune, just as range over the original preview.
func transcriptSearchNormalizedPreview(text string) string {
	var out strings.Builder
	out.Grow(min(len(text), transcriptSearchPreviewRunes+len("…")))
	runes := 0
	haveText, pendingSpace := false, false
	for offset := 0; offset < len(text); {
		start := offset
		r, width := rune(text[offset]), 1
		if r >= utf8.RuneSelf {
			r, width = utf8.DecodeRuneInString(text[offset:])
		}
		offset += width
		if transcriptSearchIsSpace(r) {
			pendingSpace = haveText
			continue
		}
		if pendingSpace {
			if runes == transcriptSearchPreviewRunes {
				out.WriteString("…")
				return out.String()
			}
			out.WriteByte(' ')
			runes++
			pendingSpace = false
		}
		if runes == transcriptSearchPreviewRunes {
			out.WriteString("…")
			return out.String()
		}
		out.WriteString(text[start:offset])
		runes++
		haveText = true
	}
	return out.String()
}
