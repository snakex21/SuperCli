// Package strutil has small string helpers.
package strutil

import "unicode"

// Reverse returns s with its runes in reverse order.
func Reverse(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

// IsPalindrome reports whether s reads the same backwards, ignoring case
// and non-letters.
func IsPalindrome(s string) bool {
	var letters []rune
	for _, r := range s {
		if unicode.IsLetter(r) {
			letters = append(letters, unicode.ToLower(r))
		}
	}
	for i, j := 0, len(letters)-1; i < j; i, j = i+1, j-1 {
		if letters[i] != letters[j] {
			return false
		}
	}
	return true
}
