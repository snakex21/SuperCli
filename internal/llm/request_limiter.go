package llm

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Client-side request limiter (config.toml `requests_per_minute`, globally
// and per [[providers]] entry). Some gateways — Cloudflare Workers AI / AI
// Gateway among them — enforce a per-minute request count that a single
// SuperCli process can reach on its own: parallel task workers, darwin
// best-of-N candidates, advisor/review/compaction side calls and provider
// retries all share one endpoint. The limiter is process-wide and keyed per
// provider endpoint, so every parallel worker draws from the same budget.
//
// It is a sliding-window token bucket: at most N request starts in any 60 s
// window (a plain refilling bucket with capacity N could still start 2N
// requests inside one minute and trip the very limit it exists to avoid).
// 0 = off, the default.

const requestLimitWindow = time.Minute

type requestLimiter struct {
	mu     sync.Mutex
	starts []time.Time // request starts inside the current window, oldest first
}

var requestLimits = struct {
	sync.Mutex
	defaultRPM int
	perKey     map[string]int
	buckets    map[string]*requestLimiter
}{perKey: map[string]int{}, buckets: map[string]*requestLimiter{}}

// limiterNow is the limiter clock; tests replace it (and retrySleep).
var limiterNow = time.Now

// SetDefaultRequestsPerMinute installs the global requests_per_minute that
// applies to every provider endpoint without its own value. n <= 0 = off.
func SetDefaultRequestsPerMinute(n int) {
	if n < 0 {
		n = 0
	}
	requestLimits.Lock()
	requestLimits.defaultRPM = n
	requestLimits.Unlock()
}

// SetRequestsPerMinute installs a per-endpoint override (a [[providers]]
// entry's requests_per_minute). n <= 0 removes the override so the global
// value applies again.
func SetRequestsPerMinute(baseURL string, n int) {
	key := RequestLimitKey(baseURL)
	if key == "" {
		return
	}
	requestLimits.Lock()
	if n > 0 {
		requestLimits.perKey[key] = n
	} else {
		delete(requestLimits.perKey, key)
	}
	requestLimits.Unlock()
}

// ResetRequestLimits clears every limit and bucket (config reload, tests).
func ResetRequestLimits() {
	requestLimits.Lock()
	requestLimits.defaultRPM = 0
	requestLimits.perKey = map[string]int{}
	requestLimits.buckets = map[string]*requestLimiter{}
	requestLimits.Unlock()
}

// RequestLimitKey normalizes a provider base URL into the limiter key: scheme
// and host lower-cased, trailing slashes and a trailing /v1 dropped, so
// "https://X/v1/" and "https://x/v1" share one budget.
func RequestLimitKey(baseURL string) string {
	raw := strings.TrimSpace(baseURL)
	if raw == "" {
		return ""
	}
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		raw = strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + u.Path
	}
	raw = strings.TrimRight(raw, "/")
	raw = strings.TrimSuffix(raw, "/v1")
	return strings.TrimRight(raw, "/")
}

// RequestsPerMinuteFor reports the effective limit for a base URL (0 = off).
func RequestsPerMinuteFor(baseURL string) int {
	key := RequestLimitKey(baseURL)
	requestLimits.Lock()
	defer requestLimits.Unlock()
	if n, ok := requestLimits.perKey[key]; ok && key != "" {
		return n
	}
	return requestLimits.defaultRPM
}

func requestBucket(key string) *requestLimiter {
	requestLimits.Lock()
	defer requestLimits.Unlock()
	b := requestLimits.buckets[key]
	if b == nil {
		b = &requestLimiter{}
		requestLimits.buckets[key] = b
	}
	return b
}

// reserve records a request start when the window has room and otherwise
// returns how long until the oldest start leaves the window.
func (b *requestLimiter) reserve(limit int, now time.Time) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	cutoff := now.Add(-requestLimitWindow)
	keep := b.starts[:0]
	for _, at := range b.starts {
		if at.After(cutoff) {
			keep = append(keep, at)
		}
	}
	b.starts = keep
	if len(b.starts) < limit {
		b.starts = append(b.starts, now)
		return 0
	}
	wait := b.starts[0].Add(requestLimitWindow).Sub(now)
	if wait <= 0 {
		wait = time.Millisecond
	}
	return wait
}

// WaitRequestSlot blocks until the endpoint's requests_per_minute budget
// allows one more request start, emitting notice (may be nil) before every
// wait. The wait selects on ctx, so a user cancel ends it immediately.
// Without a configured limit it returns at once.
func WaitRequestSlot(ctx context.Context, baseURL string, notice func(string)) error {
	limit := RequestsPerMinuteFor(baseURL)
	if limit <= 0 {
		return ctx.Err()
	}
	key := RequestLimitKey(baseURL)
	bucket := requestBucket(key)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		wait := bucket.reserve(limit, limiterNow())
		if wait == 0 {
			return nil
		}
		if notice != nil {
			notice(requestLimitNotice(key, limit, wait))
		}
		if err := retrySleep(ctx, wait); err != nil {
			return err
		}
	}
}

// requestLimitNotice is the wire text of the limiter status line; the TUI
// and web GUI localize it by pattern.
func requestLimitNotice(key string, limit int, wait time.Duration) string {
	host := key
	if u, err := url.Parse(key); err == nil && u.Host != "" {
		host = u.Host
	}
	return fmt.Sprintf("%s: request limit %d/min reached — waiting %s", host, limit, wait.Round(100*time.Millisecond))
}
