package users

import (
	"errors"
	"strings"
)

// ErrInvalid is returned for malformed create requests.
var ErrInvalid = errors.New("invalid create request")

// ParseCreate parses a create request line of the form "name".
func ParseCreate(line string) (string, error) {
	name := strings.TrimSpace(line)
	if name == "" {
		return "", ErrInvalid
	}
	return name, nil
}
