// Package humandur parses and formats human-friendly durations such as "1d2h30m".
package humandur

import (
	"errors"
	"time"
)

var (
	// ErrSyntax is returned (possibly wrapped) for malformed input.
	ErrSyntax = errors.New("humandur: invalid syntax")
	// ErrRange is returned (possibly wrapped) when the value does not fit in time.Duration.
	ErrRange = errors.New("humandur: value out of range")
)

// Parse converts a human duration string into a time.Duration.
func Parse(s string) (time.Duration, error) {
	// TODO: draft delegates to the standard library, which has different rules.
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, ErrSyntax
	}
	return d, nil
}

// Format renders d in the same notation Parse accepts.
func Format(d time.Duration) string {
	// TODO: draft delegates to the standard library.
	return d.String()
}
