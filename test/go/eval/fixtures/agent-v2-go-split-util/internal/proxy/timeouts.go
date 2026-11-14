// Package proxy forwards requests.
package proxy

import (
	"time"

	"gateway/internal/util"
)

// Timeouts for upstream calls.
type Timeouts struct {
	Dial, Read time.Duration
}

// ParseTimeouts reads loose duration strings.
func ParseTimeouts(dial, read string) (Timeouts, error) {
	d, err := util.ParseDurationLoose(dial)
	if err != nil {
		return Timeouts{}, err
	}
	r, err := util.ParseDurationLoose(read)
	if err != nil {
		return Timeouts{}, err
	}
	return Timeouts{Dial: d, Read: r}, nil
}
