// Package textwrap wraps plain text to a fixed line width.
package textwrap

import (
	"errors"
	"strings"
)

// ErrWidth is returned when width is smaller than 1.
var ErrWidth = errors.New("textwrap: width must be at least 1")

// Wrap breaks text into lines of at most width characters.
func Wrap(text string, width int) ([]string, error) {
	// TODO: draft implementation, see task description.
	if width < 1 {
		return nil, nil
	}
	var lines []string
	cur := ""
	for _, w := range strings.Fields(text) {
		for len(w) > width {
			if cur != "" {
				lines = append(lines, cur)
				cur = ""
			}
			lines = append(lines, w[:width])
			w = w[width:]
		}
		if cur == "" {
			cur = w
		} else if len(cur)+1+len(w) <= width {
			cur += " " + w
		} else {
			lines = append(lines, cur)
			cur = w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines, nil
}
