package users

import (
	"errors"
	"strings"
)

// ErrInvalid is returned for malformed create requests.
var ErrInvalid = errors.New("invalid create request")

// ParseCreate parses a create request line of the form "name,email".
func ParseCreate(line string) (name, email string, err error) {
	parts := strings.Split(line, ",")
	if len(parts) != 2 {
		return "", "", ErrInvalid
	}
	name, email = strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	if name == "" || !strings.Contains(email, "@") {
		return "", "", ErrInvalid
	}
	return name, email, nil
}
