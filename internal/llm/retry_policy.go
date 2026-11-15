package llm

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Retry policy shared by every provider transport and the agent-level
// transport/stream retry. One process-global setting (config.toml
// `max_retries`, installed by config.ApplyLLMGlobals and changed live by the
// TUI /settings panel and the web GUI settings) replaces the historical
// per-call-site constants (3 attempts for 429, 5 for 503, 3 for the Responses
// API, 2 for dropped connections, 5 for short network outages).

// DefaultMaxRetries is the number of retries after the first attempt when
// max_retries is not configured.
const DefaultMaxRetries = 5

// UnlimitedRetries is the canonical "retry until it works or the user stops
// it" value of max_retries.
const UnlimitedRetries = -1

// MaxRetryBackoff caps every single wait between two attempts, whether it
// comes from the exponential backoff or from a provider's Retry-After header.
const MaxRetryBackoff = 60 * time.Second

var (
	maxRetriesSetting  atomic.Int64
	retryUnknownErrors atomic.Bool
)

func init() { maxRetriesSetting.Store(DefaultMaxRetries) }

// SetMaxRetries installs the process-wide retry count. n >= 0 is a finite
// number of retries (0 = none), UnlimitedRetries means no limit. Any other
// negative value is invalid and restores the default.
func SetMaxRetries(n int) {
	if n < UnlimitedRetries {
		n = DefaultMaxRetries
	}
	maxRetriesSetting.Store(int64(n))
}

// MaxRetries reports the current retry count (UnlimitedRetries = no limit).
func MaxRetries() int { return int(maxRetriesSetting.Load()) }

// SetRetryUnknownErrors enables retrying provider errors that are neither
// known-transient nor known-permanent (config.toml retry_unknown_errors).
func SetRetryUnknownErrors(on bool) { retryUnknownErrors.Store(on) }

// RetryUnknownErrors reports whether unclassified provider errors are retried.
func RetryUnknownErrors() bool { return retryUnknownErrors.Load() }

// RetryAllowed reports whether another retry may follow `done` retries that
// were already made for the same request.
func RetryAllowed(done int) bool {
	limit := MaxRetries()
	return limit < 0 || done < limit
}

// RetryProgress renders "3/5" (or "12/∞" in unlimited mode) for retry number
// n of the current setting. The infinity sign is part of the wire notice and
// is matched by the TUI and web GUI localizers.
func RetryProgress(n int) string {
	limit := MaxRetries()
	if limit < 0 {
		return strconv.Itoa(n) + "/∞"
	}
	return strconv.Itoa(n) + "/" + strconv.Itoa(limit)
}

// retryJitter returns a duration in [floor, ceiling]. A seam for tests.
var retryJitter = func(floor, ceiling time.Duration) time.Duration {
	if ceiling <= floor {
		return floor
	}
	return floor + time.Duration(rand.Int64N(int64(ceiling-floor)+1))
}

// RetryBackoff is the wait before retry number n (1-based): base doubled per
// retry, capped at MaxRetryBackoff, with jitter keeping at least half of the
// nominal delay so many workers hitting the same limit do not retry in
// lockstep and a retry never degenerates into a busy loop.
func RetryBackoff(n int, base time.Duration) time.Duration {
	if n < 1 {
		n = 1
	}
	ceiling := MaxRetryBackoff
	if shift := n - 1; shift < 30 {
		if d := base << shift; d > 0 && d < MaxRetryBackoff {
			ceiling = d
		}
	}
	return retryJitter(ceiling/2, ceiling)
}

// retrySleep waits d or until ctx is cancelled. Every retry wait goes through
// it so Ctrl+C / Esc / Stop / the batch signal end a retry wait immediately,
// and tests can replace it with a fake clock.
var retrySleep = func(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

// SleepRetry is the exported cancel-aware retry wait.
func SleepRetry(ctx context.Context, d time.Duration) error { return retrySleep(ctx, d) }

// RetryClass is the retry classification of a provider error.
type RetryClass int

const (
	// RetryUnknown: neither known-transient nor known-permanent; retried only
	// with retry_unknown_errors = true.
	RetryUnknown RetryClass = iota
	// RetryTransient: rate limit, overload, temporary unavailability.
	RetryTransient
	// RetryPermanent: auth, invalid request, model not found, context
	// overflow, quota/billing — never retried regardless of the settings.
	RetryPermanent
)

func (c RetryClass) String() string {
	switch c {
	case RetryTransient:
		return "transient"
	case RetryPermanent:
		return "permanent"
	default:
		return "unknown"
	}
}

// ProviderStreamError is an error a provider reported INSIDE an accepted
// stream (an SSE error event or error chunk) rather than as an HTTP status.
// Gateways such as Cloudflare Workers AI / AI Gateway report their own
// per-minute rate limits this way. Error() keeps the historical text.
type ProviderStreamError struct {
	Prefix  string // historical text prefix, e.g. "provider stream error"
	Type    string // provider error type (e.g. "overloaded_error")
	Code    string // provider error code (e.g. "rate_limit_exceeded", "429")
	Message string
}

func (e *ProviderStreamError) Error() string {
	if e.Prefix == "" {
		return e.Message
	}
	return e.Prefix + ": " + e.Message
}

// Class classifies the in-stream error by its code/type first and its message
// text second.
func (e *ProviderStreamError) Class() RetryClass {
	if isQuotaExhaustedText(e.Message) {
		return RetryPermanent
	}
	if c := classifyErrorCode(e.Code); c != RetryUnknown {
		return c
	}
	if c := classifyErrorCode(e.Type); c != RetryUnknown {
		return c
	}
	return classifyErrorText(e.Message)
}

// ClassifyProviderError classifies an error returned by a provider. HTTP
// failures use the status (and body for the narrow model-unavailable 4xx
// case); in-stream errors use their code/type; everything else falls back to
// case-insensitive text patterns.
func ClassifyProviderError(err error) RetryClass {
	if err == nil {
		return RetryUnknown
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return RetryPermanent
	}
	var responseErr *HTTPResponseError
	if errors.As(err, &responseErr) {
		return classifyHTTPResponse(responseErr.Status, []byte(responseErr.Body))
	}
	var streamErr *ProviderStreamError
	if errors.As(err, &streamErr) {
		return streamErr.Class()
	}
	return classifyErrorText(err.Error())
}

func classifyHTTPResponse(status int, body []byte) RetryClass {
	// A daily/billing quota answers 429 too, but no retry within minutes can
	// succeed (Workers AI 3036 "daily free allocation of 10,000 neurons").
	if isQuotaExhaustedText(string(body)) {
		return RetryPermanent
	}
	if c := cloudflareEnvelopeClass(body); c != RetryUnknown {
		return c
	}
	if isRetryableProviderResponse(status, body) {
		return RetryTransient
	}
	if isPermanentHTTPStatus(status) {
		return RetryPermanent
	}
	if c := classifyErrorText(string(body)); c == RetryPermanent {
		return c
	}
	return RetryUnknown
}

// isPermanentHTTPStatus lists the client errors that can never succeed by
// sending the same request again.
func isPermanentHTTPStatus(status int) bool {
	switch status {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusPaymentRequired,
		http.StatusForbidden, http.StatusNotFound, http.StatusMethodNotAllowed,
		http.StatusGone, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
		return true
	}
	return false
}

// providerResponseRetryable is the retry gate of the provider HTTP loops:
// known-transient statuses always, unclassified ones only when the user
// opted into retry_unknown_errors. Permanent errors never.
func providerResponseRetryable(status int, body []byte) bool {
	switch classifyHTTPResponse(status, body) {
	case RetryTransient:
		return true
	case RetryUnknown:
		return RetryUnknownErrors()
	default:
		return false
	}
}

func classifyErrorCode(code string) RetryClass {
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "" {
		return RetryUnknown
	}
	if c, ok := cloudflareErrorCodes[code]; ok {
		return c
	}
	if n, err := strconv.Atoi(code); err == nil {
		if isRetryableHTTPStatus(n) {
			return RetryTransient
		}
		if isPermanentHTTPStatus(n) {
			return RetryPermanent
		}
		return RetryUnknown
	}
	switch code {
	case "rate_limit_error", "rate_limit_exceeded", "rate_limited", "ratelimit", "too_many_requests",
		"overloaded_error", "overloaded", "server_overloaded", "service_unavailable", "server_busy",
		"engine_overloaded", "capacity_exceeded", "model_overloaded", "temporarily_unavailable", "timeout":
		return RetryTransient
	case "invalid_request_error", "invalid_request", "authentication_error", "permission_error",
		"permission_denied", "not_found_error", "model_not_found", "invalid_api_key", "unauthorized",
		"context_length_exceeded", "insufficient_quota", "billing_error", "request_too_large",
		"string_above_max_length", "invalid_prompt", "content_filter", "unsupported_parameter":
		return RetryPermanent
	}
	return RetryUnknown
}

// permanentErrorPhrases veto a retry even when a transient phrase also
// matches (e.g. "invalid request: too many tokens for rate limit tier").
var permanentErrorPhrases = []string{
	"context length", "context_length", "context window", "maximum context", "prompt is too long",
	"too many tokens", "reduce the length", "exceeds the context", "input is too long",
	"invalid api key", "invalid_api_key", "incorrect api key", "unauthorized", "authentication",
	"permission denied", "forbidden", "insufficient_quota", "exceeded your current quota",
	"billing", "credit balance", "does not exist", "model not found", "model_not_found",
	"no such model", "unknown model", "invalid request", "invalid_request",
	"http 400", "http 401", "http 402", "http 403", "http 404", "http 422",
}

var transientErrorPhrases = []string{
	"rate limit", "rate-limit", "ratelimit", "rate limiting", "rate_limit", "per min rate",
	"requests per minute", "too many requests", "overloaded", "temporarily unavailable",
	"service unavailable", "server is busy", "server busy", "capacity", "try again later",
	"please try again", "upstream connect error", "bad gateway", "gateway timeout",
	"http 429", "http 500", "http 502", "http 503", "http 504", "http 529",
}

func classifyErrorText(text string) RetryClass {
	text = strings.ToLower(text)
	if strings.TrimSpace(text) == "" {
		return RetryUnknown
	}
	if isQuotaExhaustedText(text) {
		return RetryPermanent
	}
	for _, phrase := range permanentErrorPhrases {
		if strings.Contains(text, phrase) {
			return RetryPermanent
		}
	}
	for _, phrase := range transientErrorPhrases {
		if strings.Contains(text, phrase) {
			return RetryTransient
		}
	}
	return RetryUnknown
}

// ShouldRetryProviderError applies the user settings to a classification:
// transient errors always (within max_retries), unknown ones only with
// retry_unknown_errors, permanent ones never.
func ShouldRetryProviderError(err error) bool {
	switch ClassifyProviderError(err) {
	case RetryTransient:
		return true
	case RetryUnknown:
		return RetryUnknownErrors()
	default:
		return false
	}
}

// providerRetryWait picks the wait before provider retry number n and
// reports whether the retry should happen at all. A 429 whose explicit
// Retry-After exceeds MaxRetryBackoff is a quota window, not a burst: it is
// surfaced at once so an explicitly configured fallback can take over, even
// in unlimited mode.
//
// Without Retry-After the wait is exponential backoff with jitter; a body
// that names a per-minute limit uses PerMinuteRetryBase so the retries reach
// the next minute instead of hammering the same window.
func providerRetryWait(status int, h http.Header, body []byte, n int) (time.Duration, bool) {
	retryAfter, ok := parseRetryAfter(h)
	if !ok {
		retryAfter, ok = bodyRetryAfter(body)
	}
	if ok && status == http.StatusTooManyRequests && retryAfter > MaxRetryBackoff {
		return 0, false
	}
	if ok {
		if retryAfter > MaxRetryBackoff {
			retryAfter = MaxRetryBackoff
		}
		return retryAfter, true
	}
	if isPerMinuteLimitText(string(body)) {
		return RetryBackoff(n, PerMinuteRetryBase), true
	}
	return RetryBackoff(n, 500*time.Millisecond), true
}
