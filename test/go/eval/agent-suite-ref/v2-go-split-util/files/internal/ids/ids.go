// Package ids generates identifiers.
package ids

import (
	"fmt"
	"sync/atomic"
)

var seq atomic.Uint64

// New returns prefix_000001, prefix_000002, ...
func New(prefix string) string {
	return fmt.Sprintf("%s_%06d", prefix, seq.Add(1))
}
