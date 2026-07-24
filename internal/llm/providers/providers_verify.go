package providers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

// verifyTimeout caps the post-configuration test request. Local
// servers answer in a second or two; cloud providers within a few.
const verifyTimeout = 15 * time.Second

// VerifyConnection sends a tiny test completion ("Say OK") to the
// provider and returns nil on success or a human-readable error
// explaining what to check. Used by the first-run wizard and
// /providers add right after the user picks provider+model.
func VerifyConnection(ctx context.Context, baseURL, apiKey, model string) error {
	return VerifyConnectionForProvider(ctx, config.ProviderOpenAI, baseURL, apiKey, model)
}

// VerifyConnectionForProvider uses the provider's actual wire API for the
// post-configuration smoke test. In particular, Responses providers must not
// be falsely rejected by probing /chat/completions.
func VerifyConnectionForProvider(ctx context.Context, providerType, baseURL, apiKey, model string) error {
	ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	if providerType == config.ProviderResponses {
		p, err := llm.NewResponses(llm.ResponsesConfig{BaseURL: baseURL, APIKey: apiKey, Model: model})
		if err != nil {
			return humanizeVerifyError(err.Error(), baseURL, apiKey)
		}
		ch, err := p.Complete(ctx, []llm.Message{{Role: llm.RoleUser, Content: "Reply with the single token OK."}}, nil)
		if err != nil {
			return humanizeVerifyError(err.Error(), baseURL, apiKey)
		}
		for d := range ch {
			if d.Err != nil {
				return humanizeVerifyError(d.Err.Error(), baseURL, apiKey)
			}
		}
		return nil
	}
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
	case hasVerifyHTTPStatus(low, 401) || hasVerifyHTTPStatus(low, 403):
		if apiKey == "" {
			hint = "the server requires an API key — add one"
		} else {
			hint = "the API key was rejected — check it for typos or expiry"
		}
	case hasVerifyHTTPStatus(low, 404):
		hint = "endpoint not found — check the base URL (it usually ends with /v1)"
	case hasVerifyHTTPStatus(low, 429):
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

func hasVerifyHTTPStatus(raw string, status int) bool {
	code := fmt.Sprint(status)
	return strings.Contains(raw, "status "+code) || strings.Contains(raw, "http "+code)
}
