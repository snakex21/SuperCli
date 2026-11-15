package meter

import (
	"strings"
	"unicode"
)

// normalizeModel upper-cases a model name and drops separators such as
// dashes, dots and spaces.
func normalizeModel(model string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) {
			return unicode.ToUpper(r)
		}
		return -1
	}, model)
}
