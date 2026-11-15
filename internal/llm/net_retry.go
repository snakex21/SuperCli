package llm

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// HTTPResponseError preserves response metadata that higher-level routing
// needs to make a useful decision. In particular, an ordered fallback chain
// must be able to keep an HTTP-429 backend asleep for the provider's full
// Retry-After interval instead of probing it again every few seconds.
// Error intentionally keeps the historical text format because it is shown
// directly by the TUI/Web GUI and some callers match its actionable hint.
type HTTPResponseError struct {
	Status        int
	Body          string
	Hint          string
	RetryAfter    time.Duration
	HasRetryAfter bool
}

func (e *HTTPResponseError) Error() string {
	if e == nil {
		return "http response error"
	}
	body := strings.TrimSpace(e.Body)
	// Gateway failures often attach several kilobytes of routing diagnostics to
	// a one-line error. Keep the metadata on the typed error for routing, but do
	// not dump it into the chat UI.
	if len(body) > 512 {
		if message := strings.TrimSpace(errorTextFromBody([]byte(body))); message != "" {
			body = message
		}
	}
	return fmt.Sprintf("http %d: %s%s", e.Status, body, e.Hint)
}

func newHTTPResponseError(status int, body []byte, hint string, headers http.Header) error {
	retryAfter, hasRetryAfter := parseRetryAfter(headers)
	if !hasRetryAfter {
		retryAfter, hasRetryAfter = bodyRetryAfter(body)
	}
	return &HTTPResponseError{
		Status:        status,
		Body:          string(body),
		Hint:          hint,
		RetryAfter:    retryAfter,
		HasRetryAfter: hasRetryAfter,
	}
}

// RateLimitRetryAfter reports whether err is an HTTP 429 and, when supplied
// by the server, how long the backend asked us to wait. The boolean describes
// the error class; a zero duration can therefore mean a genuine 429 without a
// usable Retry-After header.
func RateLimitRetryAfter(err error) (time.Duration, bool) {
	var responseErr *HTTPResponseError
	if !errors.As(err, &responseErr) || responseErr.Status != http.StatusTooManyRequests {
		return 0, false
	}
	if !responseErr.HasRetryAfter {
		return 0, true
	}
	return responseErr.RetryAfter, true
}

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

// isRetryableProviderResponse extends the status-only policy for one common
// reseller failure mode: a gateway returns HTTP 400/422 while its nested
// upstream says the selected model is temporarily unavailable. Treating that
// as a permanent client error makes NestCafe die immediately even though the
// exact same request often succeeds a few hundred milliseconds later.
//
// Keep this deliberately narrow. "does not exist", auth failures, bad
// parameters, malformed payloads, etc. remain non-retryable 4xx errors.
func isRetryableProviderResponse(status int, body []byte) bool {
	if isRetryableHTTPStatus(status) {
		return true
	}
	if status != http.StatusBadRequest && status != http.StatusUnprocessableEntity {
		return false
	}
	text := strings.ToLower(errorTextFromBody(body))
	if text == "" {
		return false
	}
	for _, phrase := range []string{
		"model is unavailable",
		"model currently unavailable",
		"model temporarily unavailable",
	} {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

// rateLimitNotice renders the user-visible status line emitted (as
// Delta.Notice) before sleeping on a 429/5xx retry, so the UI shows
// "waiting for the provider" instead of appearing hung. retry is the 1-based
// retry number; the progress reads "3/5" or "12/∞" (max_retries = -1). The
// English wire text is localized by the TUI (localizeNotice) and the web GUI
// (SYSTEM_TEXTS); keep the three formats in sync.
func rateLimitNotice(model string, status int, wait time.Duration, retry int) string {
	kind := fmt.Sprintf("temporary provider error (HTTP %d)", status)
	if status == http.StatusTooManyRequests {
		kind = "rate limited (HTTP 429)"
	} else if status/100 == 5 {
		kind = fmt.Sprintf("provider error (HTTP %d)", status)
	}
	return fmt.Sprintf("%s: %s — retrying in %s (retry %s)",
		model, kind, wait.Round(100*time.Millisecond), RetryProgress(retry))
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
