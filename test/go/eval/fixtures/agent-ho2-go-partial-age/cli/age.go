package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// parseAge reads a positive age: a whole number followed by a unit,
// h (hours), d (days) or w (weeks). A bare number means days.
func parseAge(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	unit := 24 * time.Hour
	switch {
	case strings.HasSuffix(s, "h"):
		unit, s = time.Hour, strings.TrimSuffix(s, "h")
	case strings.HasSuffix(s, "d"):
		s = strings.TrimSuffix(s, "d")
	case strings.HasSuffix(s, "w"):
		unit, s = 7*24*time.Hour, strings.TrimSuffix(s, "w")
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("bad age %q: want e.g. 36h, 10d, 2w or a number of days", s)
	}
	return time.Duration(n) * unit, nil
}
