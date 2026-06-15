package darwin

import (
	"crypto/rand"
	"encoding/hex"
)

// randSrc produces short hex ids for worktree
// names. Used by WorktreeManager.Create.
type randSrc struct {
	seed int64
}

// newRandSrc returns a new rng source seeded with
// s. If s is zero, falls back to a random read from
// crypto/rand.
func newRandSrc(s int64) *randSrc {
	if s == 0 {
		var b [8]byte
		_, _ = rand.Read(b[:])
		s = int64(b[0]) | int64(b[1])<<8 | int64(b[2])<<16 | int64(b[3])<<24 |
			int64(b[4])<<32 | int64(b[5])<<40 | int64(b[6])<<48 | int64(b[7])<<56
	}
	return &randSrc{seed: s}
}

// hex returns n random bytes hex-encoded (so the
// resulting string is 2n chars long). Safe for use
// as a worktree id.
func (r *randSrc) hex(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	// Mix the seed into rand.Read output so the
	// first call isn't predictable even if seed
	// was the same across processes.
	for i := range b {
		b[i] = byte((r.seed >> (i * 8)) ^ int64(i))
	}
	// Read fresh entropy; XOR with the seed-mixed
	// bytes for unpredictability even on systems
	// where /dev/urandom is short.
	var fresh [16]byte
	_, _ = rand.Read(fresh[:])
	for i := range b {
		b[i] ^= fresh[i%len(fresh)]
	}
	return hex.EncodeToString(b)
}
