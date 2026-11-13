package limits

import (
	"sync"
	"time"
)

// Buckets holds one token bucket per API key.
type Buckets struct {
	mu      sync.Mutex
	perSec  float64
	burst   float64
	buckets map[string]*bucket
	now     func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewBuckets allows ratePerMinute sustained requests per key with the given burst.
func NewBuckets(ratePerMinute, burst int) *Buckets {
	return &Buckets{perSec: float64(ratePerMinute) / 60, burst: float64(burst), buckets: map[string]*bucket{}, now: time.Now}
}

// Allow refills the key's bucket and takes one token if available.
func (b *Buckets) Allow(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	bk, ok := b.buckets[key]
	if !ok {
		bk = &bucket{tokens: b.burst, last: now}
		b.buckets[key] = bk
	}
	bk.tokens += now.Sub(bk.last).Seconds() * b.perSec
	if bk.tokens > b.burst {
		bk.tokens = b.burst
	}
	bk.last = now
	if bk.tokens < 1 {
		return false
	}
	bk.tokens--
	return true
}
