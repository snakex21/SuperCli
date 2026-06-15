package providers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"supercli/internal/llm"
)

// verifyTimeout caps the post-configuration test request. Local
// servers answer in a second or two; cloud providers within a few.
const verifyTimeout = 15 * time.Second

// VerifyConnection sends a tiny test completion ("Say OK") to the
// provider and returns nil on success or a human-readable error
// explaining what to check. Used by the first-run wizard and
// /providers add right after the user picks provider+model.
func VerifyConnection(ctx context.Context, baseURL, apiKey, model string) error {
	ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	res, err := llm.Probe(ctx, baseURL, apiKey, model)
	if err != nil {
		return humanizeVerifyError(err.Error(), baseURL, apiKey)
	}
	if res.Error != "" {
		return humanizeVerifyError(res.Error, baseURL, apiKey)
	}
	return nil
}

// humanizeVerifyError converts raw HTTP/network errors into
// actionable advice.
func humanizeVerifyError(raw, baseURL, apiKey string) error {
	low := strings.ToLower(raw)
	hint := ""
	switch {
	case strings.Contains(low, "status 401") || strings.Contains(low, "status 403"):
		if apiKey == "" {
			hint = "the server requires an API key — add one"
		} else {
			hint = "the API key was rejected — check it for typos or expiry"
		}
	case strings.Contains(low, "status 404"):
		hint = "endpoint not found — check the base URL (it usually ends with /v1)"
	case strings.Contains(low, "status 429"):
		hint = "rate limited — wait a moment or check your plan limits"
	case strings.Contains(low, "model") && (strings.Contains(low, "not found") || strings.Contains(low, "does not exist") || strings.Contains(low, "status 400")):
		hint = "the model may not be available on this server — pick another model"
	case strings.Contains(low, "connection refused") || strings.Contains(low, "connectex") || strings.Contains(low, "no such host") || strings.Contains(low, "dial tcp"):
		hint = fmt.Sprintf("nothing is listening at %s — is the server running?", baseURL)
	case strings.Contains(low, "deadline exceeded") || strings.Contains(low, "timeout"):
		hint = "the server did not answer in time — it may still be loading a model"
	}
	if len(raw) > 200 {
		raw = raw[:200] + "..."
	}
	if hint != "" {
		return fmt.Errorf("%s (%s)", hint, raw)
	}
	return fmt.Errorf("connection test failed: %s", raw)
}
