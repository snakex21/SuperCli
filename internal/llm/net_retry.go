package llm

import (
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

// rateLimitWaitBudget caps the TOTAL time a single Complete call may
// spend sleeping between rate-limit/5xx retries. A provider that asks
// for a longer Retry-After than the remaining budget gets clamped: we
// still retry once more after the clamped wait, and if the limit
// persists the run fails fast with an actionable error instead of
// hanging for minutes.
const rateLimitWaitBudget = 10 * time.Second

// parseRetryAfter reads the Retry-After response header. Both RFC
// 9110 forms are supported: delta-seconds ("120") and HTTP-date
// ("Fri, 31 Dec 1999 23:59:59 GMT"). Returns (0, false) when the
// header is absent or unparsable; negative values (a date in the
// past) are reported as zero so callers retry immediately.
func parseRetryAfter(h http.Header) (time.Duration, bool) {
	v := h.Get("Retry-After")
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0, true
		}
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		d := time.Until(t)
		if d < 0 {
			d = 0
		}
		return d, true
	}
	return 0, false
}

// retryWait picks the sleep before retry number attempt (1-based
// count of attempts already made). The Retry-After header wins when
// present; otherwise the historical exponential backoff (0.5s, 1s,
// …) applies. Whatever the source, the wait is clamped so that the
// total slept time across the request never exceeds
// rateLimitWaitBudget. The remaining budget must be tracked by the
// caller and passed in; the returned wait is guaranteed <= remaining.
func retryWait(h http.Header, attempt int, remaining time.Duration) time.Duration {
	wait, ok := parseRetryAfter(h)
	if !ok {
		ceiling := 500 * time.Millisecond << (attempt - 1)
		// Jitter prevents many workers that hit the same reseller limit from
		// retrying in lockstep. Keep at least half the historical delay so the
		// retry remains a real backoff rather than an immediate spin.
		floor := ceiling / 2
		wait = floor + time.Duration(rand.Int64N(int64(ceiling-floor)+1))
	}
	if wait > remaining {
		wait = remaining
	}
	if wait < 0 {
		wait = 0
	}
	return wait
}

// isRetryableHTTPStatus is deliberately conservative. Permanent client
// errors must return immediately; only transient gateway, timeout and
// throttling statuses get the bounded retry budget.
func isRetryableHTTPStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, // 408
		http.StatusTooEarly,        // 425
		http.StatusTooManyRequests, // 429
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
		520, 521, 522, 523, 524, 529:
		return true
	default:
		return false
	}
}

// rateLimitNotice renders the user-visible status line emitted (as
// Delta.Notice) before sleeping on a 429/5xx retry, so the UI shows
// "waiting for the provider" instead of appearing hung.
func rateLimitNotice(model string, status int, wait time.Duration, attempt, maxAttempts int) string {
	kind := fmt.Sprintf("temporary provider error (HTTP %d)", status)
	if status == http.StatusTooManyRequests {
		kind = "rate limited (HTTP 429)"
	} else if status/100 == 5 {
		kind = fmt.Sprintf("provider error (HTTP %d)", status)
	}
	return fmt.Sprintf("%s: %s — retrying in %s (attempt %d/%d)",
		model, kind, wait.Round(100*time.Millisecond), attempt+1, maxAttempts)
}

// rateLimitExhaustedHint is appended to the terminal error after all
// 429 retries are used up, so the user knows the model (not the
// tool) is the bottleneck and what to do about it.
func rateLimitExhaustedHint(model string, status int) string {
	if status != http.StatusTooManyRequests {
		return ""
	}
	return fmt.Sprintf(" — model %q is rate-limited by the provider; consider switching model (/model) or waiting", model)
}
