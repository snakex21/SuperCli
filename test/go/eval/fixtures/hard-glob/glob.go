// Package glob implements a small shell-style pattern matcher.
package glob

import (
	"errors"
	"strings"
)

// ErrBadPattern is returned when a pattern is malformed.
var ErrBadPattern = errors.New("glob: malformed pattern")

// Match reports whether the whole name matches the whole pattern.
func Match(pattern, name string) (bool, error) {
	// TODO: draft only understands literal patterns and a single trailing '*'.
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(name, strings.TrimSuffix(pattern, "*")), nil
	}
	return pattern == name, nil
}
