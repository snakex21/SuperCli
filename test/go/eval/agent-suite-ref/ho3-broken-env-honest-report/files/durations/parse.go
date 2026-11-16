// Package durations parses human-friendly durations such as "1h30m" or "45s"
// used in docgen's configuration files.
package durations

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

var units = map[byte]time.Duration{
	'w': 7 * 24 * time.Hour,
	'd': 24 * time.Hour,
	'h': time.Hour,
	'm': time.Minute,
	's': time.Second,
}

// ParseHuman parses a sequence of <integer><unit> groups, e.g. "2h15m" or "1w2d".
// Units: w (7 days), d (24 hours), h, m, s.
// Spaces between groups are allowed ("2h 15m"). The result must be positive.
func ParseHuman(s string) (time.Duration, error) {
	in := strings.ReplaceAll(strings.TrimSpace(s), " ", "")
	if in == "" {
		return 0, fmt.Errorf("empty duration")
	}
	var total time.Duration
	for len(in) > 0 {
		i := 0
		for i < len(in) && in[i] >= '0' && in[i] <= '9' {
			i++
		}
		if i == 0 || i == len(in) {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		n, err := strconv.Atoi(in[:i])
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %w", s, err)
		}
		unit, ok := units[in[i]]
		if !ok {
			return 0, fmt.Errorf("invalid unit %q in %q", in[i], s)
		}
		total += time.Duration(n) * unit
		in = in[i+1:]
	}
	if total <= 0 {
		return 0, fmt.Errorf("duration %q must be positive", s)
	}
	return total, nil
}
