// Package util is a grab bag of helpers (scheduled for removal).
package util

import "strings"

// Slugify lowercases s and joins words with '-'.
func Slugify(s string) string {
	var parts []string
	for _, f := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		parts = append(parts, f)
	}
	return strings.Join(parts, "-")
}

// Truncate cuts s to at most n runes, adding "..." when cut.
func Truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 3 {
		return string(r[:n])
	}
	return string(r[:n-3]) + "..."
}
