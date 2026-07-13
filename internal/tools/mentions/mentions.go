// Package mentions parses @file references from user input,
// reads the referenced files, and returns context blocks
// that get prepended to the prompt before the LLM sees it.
package mentions

import (
	"fmt"
	"os"
	"strings"
	"unicode"

	"supercli/internal/tools/sandbox"
)

// Mention is a single @file reference parsed from user input.
type Mention struct {
	// Path is the raw path from the input (e.g. "src/main.go").
	Path string
	// Abs is the resolved absolute path.
	Abs string
	// Content is the file's text content (truncated to maxBytes).
	Content string
	// Tokens is a rough estimate of the token count (len/4).
	Tokens int
}

// Parse extracts all @path mentions from the input text.
// A mention starts with '@' followed by a non-whitespace path.
// The path ends at whitespace, comma, or end-of-string.
// Returns the remaining text (with @mentions stripped) and
// the list of parsed mentions.
//
// Examples:
//
//	"@src/main.go refactor this" → remaining="refactor this", mentions=["src/main.go"]
//	"@a.go @b.go merge them" → remaining="merge them", mentions=["a.go","b.go"]
func Parse(text string) (string, []string) {
	var paths []string
	var result strings.Builder
	i := 0
	for i < len(text) {
		if text[i] == '@' && (i == 0 || unicode.IsSpace(rune(text[i-1])) || text[i-1] == ',') {
			// Start of a mention.
			j := i + 1
			for j < len(text) && !unicode.IsSpace(rune(text[j])) && text[j] != ',' {
				j++
			}
			if j > i+1 {
				paths = append(paths, text[i+1:j])
				i = j
				// Support compact comma-separated mentions:
				// @a.go,@b.go,@c.go task. The comma is a
				// separator, not user text, and the following
				// @ should be treated as a new mention even
				// though it is not preceded by whitespace.
				for i < len(text) && text[i] == ',' {
					i++
				}
				continue
			}
		}
		result.WriteByte(text[i])
		i++
	}
	return strings.TrimSpace(result.String()), paths
}

// Resolve reads each mentioned file and returns Mention
// structs with content and token estimates. Paths are
// resolved relative to home. Files larger than maxBytes
// are truncated. Missing files produce an error Mention
// (Content contains the error message).
func Resolve(home string, paths []string, maxBytes int) []Mention {
	if maxBytes <= 0 {
		maxBytes = 64 * 1024 // 64 KB default
	}
	out := make([]Mention, 0, len(paths))
	for _, p := range paths {
		m := Mention{Path: p}
		abs, err := sandbox.ResolveSafe(home, p)
		if err != nil {
			m.Content = fmt.Sprintf("(error reading @%s: %v)", p, err)
			out = append(out, m)
			continue
		}
		m.Abs = abs

		data, err := os.ReadFile(abs)
		if err != nil {
			m.Content = fmt.Sprintf("(error reading @%s: %v)", p, err)
			m.Tokens = 0
			out = append(out, m)
			continue
		}
		if len(data) > maxBytes {
			data = data[:maxBytes]
			m.Content = string(data) + fmt.Sprintf("\n... (truncated at %d bytes)", maxBytes)
		} else {
			m.Content = string(data)
		}
		// Rough token estimate: ~4 chars per token.
		m.Tokens = (len(m.Content) + 3) / 4
		out = append(out, m)
	}
	return out
}

// FormatBlock produces a context block that gets prepended
// to the user's prompt. Each file gets a clear header so
// the LLM knows it's file context, not user instructions.
//
// Output example:
//
//	--- @src/main.go (342 tokens) ---
//	package main
//	...
//	--- end ---
//
//	Refactor this function
func FormatBlock(mentions []Mention, remaining string) string {
	if len(mentions) == 0 {
		return remaining
	}
	var b strings.Builder
	for _, m := range mentions {
		fmt.Fprintf(&b, "--- @%s (%d tokens) ---\n", m.Path, m.Tokens)
		b.WriteString(m.Content)
		if !strings.HasSuffix(m.Content, "\n") {
			b.WriteByte('\n')
		}
		b.WriteString("--- end ---\n\n")
	}
	b.WriteString(remaining)
	return b.String()
}

// TotalTokens returns the sum of token estimates across all mentions.
func TotalTokens(mentions []Mention) int {
	total := 0
	for _, m := range mentions {
		total += m.Tokens
	}
	return total
}
