package limits

import "time"

// FixedWindow is the old per-minute counter limiter.
//
// Deprecated: replaced by Buckets in v2; kept only for the migration tool
// and not used by the server.
type FixedWindow struct {
	limit  int
	counts map[string]int
	window time.Time
}

// AllowRequest reports whether key is under the per-minute limit.
func (f *FixedWindow) AllowRequest(key string, now time.Time) bool {
	if now.Sub(f.window) > time.Minute {
		f.window, f.counts = now, map[string]int{}
	}
	f.counts[key]++
	return f.counts[key] <= f.limit
}
