// Package semver compares Semantic Versioning 2.0.0 version strings.
package semver

import "errors"

// ErrInvalid is returned (possibly wrapped) when a version string is malformed.
var ErrInvalid = errors.New("semver: invalid version")

// Compare reports the precedence of a relative to b: -1 if a < b, 0 if both
// have equal precedence, +1 if a > b. If either version is malformed it
// returns 0 and an error that wraps ErrInvalid.
func Compare(a, b string) (int, error) {
	// TODO: implement Semantic Versioning precedence.
	if a == b {
		return 0, nil
	}
	return -1, nil
}
