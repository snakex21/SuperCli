package llm

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// Cloudflare Workers AI / AI Gateway error handling.
//
// Workers AI answers with the Cloudflare API envelope
//
//	{"success":false,"errors":[{"code":3040,"message":"Capacity temporarily exceeded, please try again."}],"result":null}
//
// both as an HTTP error body and, on the OpenAI-compatible endpoint
// (https://api.cloudflare.com/client/v4/accounts/<id>/ai/v1), sometimes as a
// data line inside an accepted SSE stream. The per-minute inference limit
// arrives as text inside the stream ("AiError: AiError: rate limiting:
// inference request per min rate reached (<uuid>)"); AI Gateway prefixes its
// own failures with "AiGatewayError: <code>: <message>".
//
// Internal codes (developers.cloudflare.com/workers-ai/platform/errors):
// 3040 capacity temporarily exceeded (429, transient), 3007 timeout (408,
// transient), 3036 daily free allocation of neurons used up (429, NOT
// transient — it resets the next day), 5035 paid plan required, 5007/3042
// unknown or invalid model, 5018/3041/5016/3023 access denied or terms not
// accepted, 5028 model retired, 3006 request too large, 3003/5004 invalid
// input — all permanent. AI Gateway 2018 is its unified-billing rate limit
// (429, transient).
var cloudflareErrorCodes = map[string]RetryClass{
	"3040": RetryTransient, "3007": RetryTransient, "2018": RetryTransient,
	"3036": RetryPermanent, "5035": RetryPermanent, "5007": RetryPermanent, "3042": RetryPermanent,
	"5018": RetryPermanent, "3041": RetryPermanent, "5016": RetryPermanent, "3023": RetryPermanent,
	"5028": RetryPermanent, "3006": RetryPermanent, "3003": RetryPermanent, "5004": RetryPermanent,
	"5019": RetryPermanent, "5005": RetryPermanent, "3039": RetryPermanent,
}

// quotaExhaustedPhrases mark a 429 that will not clear within minutes: a
// daily or billing quota. Retrying it — let alone forever with
// max_retries = -1 — only burns requests, so it is permanent even though the
// HTTP status says "too many requests".
var quotaExhaustedPhrases = []string{
	"daily free allocation", "free allocation of", "neurons", "workers paid plan", "requires a paid plan",
	"exceeded your current quota", "insufficient_quota", "billing hard limit", "credit balance is too low",
}

func isQuotaExhaustedText(text string) bool {
	text = strings.ToLower(text)
	for _, phrase := range quotaExhaustedPhrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

// perMinuteLimitPhrases recognize a per-minute request/token window (the
// Workers AI "inference request per min rate", OpenAI "requests per min
// (RPM)", Anthropic "per-minute rate limit"). Without a Retry-After header a
// sub-second exponential backoff would spend every retry inside the same
// minute, so these use a longer base that crosses into the next window.
var perMinuteLimitPhrases = []string{
	"per min", "per-min", "per minute", "/min", "rpm", "tpm", "requests per 60",
}

// PerMinuteRetryBase is the backoff base for per-minute limits: jittered
// waits of 12.5–25 s, 25–50 s, 30–60 s, 30–60 s… — over 60 s after three
// retries, so the default five retries always reach the next minute.
const PerMinuteRetryBase = 25 * time.Second

func isPerMinuteLimitText(text string) bool {
	text = strings.ToLower(text)
	if !strings.Contains(text, "rate") && !strings.Contains(text, "limit") {
		return false
	}
	for _, phrase := range perMinuteLimitPhrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

// IsPerMinuteRateLimit reports whether err is a per-minute rate limit.
func IsPerMinuteRateLimit(err error) bool {
	return err != nil && isPerMinuteLimitText(err.Error())
}

// RetryBackoffFor is RetryBackoff with the per-minute window applied: a
// per-minute rate limit waits on PerMinuteRetryBase instead of base.
func RetryBackoffFor(err error, n int, base time.Duration) time.Duration {
	if IsPerMinuteRateLimit(err) && base < PerMinuteRetryBase {
		base = PerMinuteRetryBase
	}
	return RetryBackoff(n, base)
}

// cloudflareErrors decodes the Cloudflare API envelope's errors array.
// ok is false when the payload has no such array.
type cloudflareError struct {
	Code    string
	Message string
}

func cloudflareEnvelopeErrors(body []byte) ([]cloudflareError, bool) {
	var envelope struct {
		Errors []struct {
			Code    any    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal(body, &envelope) != nil || len(envelope.Errors) == 0 {
		return nil, false
	}
	out := make([]cloudflareError, 0, len(envelope.Errors))
	for _, e := range envelope.Errors {
		out = append(out, cloudflareError{Code: jsonScalarString(e.Code), Message: strings.TrimSpace(e.Message)})
	}
	return out, true
}

// cloudflareEnvelopeClass classifies an envelope by its first known code.
func cloudflareEnvelopeClass(body []byte) RetryClass {
	errs, ok := cloudflareEnvelopeErrors(body)
	if !ok {
		return RetryUnknown
	}
	for _, e := range errs {
		if c, known := cloudflareErrorCodes[e.Code]; known {
			return c
		}
	}
	return RetryUnknown
}

// cloudflareStreamError types an in-stream envelope ({"errors":[…]}).
func cloudflareStreamError(errs []cloudflareError) error {
	first := errs[0]
	messages := make([]string, 0, len(errs))
	for _, e := range errs {
		if e.Message == "" {
			continue
		}
		if e.Code != "" {
			messages = append(messages, e.Message+" (code "+e.Code+")")
		} else {
			messages = append(messages, e.Message)
		}
	}
	msg := strings.Join(messages, "; ")
	if msg == "" {
		msg = "stream error"
		if first.Code != "" {
			msg += " (code " + first.Code + ")"
		}
	}
	return &ProviderStreamError{Prefix: "provider stream error", Code: first.Code, Message: msg}
}

// bodyRetryAfter reads a retry hint from an error body when the
// Retry-After header is missing (proxies sometimes strip it). Logfare sends
// {"error":{"type":"rate_limit_error","retry_after":23,…}}; others use a
// top-level retry_after. Seconds; negative values mean "now".
func bodyRetryAfter(body []byte) (time.Duration, bool) {
	if len(body) == 0 {
		return 0, false
	}
	var payload struct {
		RetryAfter any `json:"retry_after"`
		Error      struct {
			RetryAfter any `json:"retry_after"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return 0, false
	}
	for _, v := range []any{payload.Error.RetryAfter, payload.RetryAfter} {
		var secs float64
		switch x := v.(type) {
		case float64:
			secs = x
		case string:
			parsed, err := time.ParseDuration(strings.TrimSpace(x))
			if err != nil {
				n, convErr := strconv.ParseFloat(strings.TrimSpace(x), 64)
				if convErr != nil {
					continue
				}
				parsed = time.Duration(n * float64(time.Second))
			}
			secs = parsed.Seconds()
		default:
			continue
		}
		if secs < 0 {
			secs = 0
		}
		return time.Duration(secs * float64(time.Second)), true
	}
	return 0, false
}
