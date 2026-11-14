package util

import (
	"fmt"
	"sync/atomic"
)

var seq atomic.Uint64

// NewID returns prefix_000001, prefix_000002, ...
func NewID(prefix string) string {
	return fmt.Sprintf("%s_%06d", prefix, seq.Add(1))
}
