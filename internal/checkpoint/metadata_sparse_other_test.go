//go:build !windows

package checkpoint

import (
	"os"
	"testing"
)

// Truncate creates the synthetic hole without writing the intervening bytes.
func markMetadataSparseFixture(t *testing.T, f *os.File) {
	t.Helper()
}
