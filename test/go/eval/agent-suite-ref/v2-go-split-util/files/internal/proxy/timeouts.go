// Package proxy forwards requests.
package proxy

import (
	"time"

	"gateway/internal/clock"
)

// Timeouts for upstream calls.
type Timeouts struct {
	Dial, Read time.Duration
}

// ParseTimeouts reads loose duration strings.
func ParseTimeouts(dial, read string) (Timeouts, error) {
	d, err := clock.ParseDurationLoose(dial)
	if err != nil {
		return Timeouts{}, err
	}
	r, err := clock.ParseDurationLoose(read)
	if err != nil {
		return Timeouts{}, err
	}
	return Timeouts{Dial: d, Read: r}, nil
}
