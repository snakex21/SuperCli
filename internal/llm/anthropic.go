package llm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const anthropicVersion = "2023-06-01"

// NormalizeAnthropicBaseURL canonicalizes a user-supplied Anthropic base URL to
// the API version root (e.g. ".../v1"), so callers can safely append
// "/messages" or "/models" without producing a doubled path.
//
// Anthropic's documented endpoint is https://api.anthropic.com/v1/messages, and
// many Anthropic-compatible proxies advertise the full ".../v1/messages" URL.
// Users naturally paste that whole URL as the provider base URL. Without this
// normalization the request path becomes ".../v1/messages/messages" (404) and
// model discovery hits ".../v1/messages/v1/models" (404) — i.e. "the Anthropic
// endpoint doesn't work". Stripping a trailing "/messages" (and any trailing
// slash) makes both the paste-the-endpoint and paste-the-root forms work.
func NormalizeAnthropicBaseURL(base string) string {
	base = strings.TrimSpace(base)
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/messages") {
		base = strings.TrimSuffix(base, "/messages")
		base = strings.TrimRight(base, "/")
	}
	return base
}

// AnthropicConfig configures the native Anthropic Messages API provider.
type AnthropicConfig struct {
	BaseURL   string
	APIKey    string
	Model     string
	MaxTokens int
	// Timeout bounds response-header wait and the maximum idle gap between
	// SSE bytes. It is not a whole-request cap. Defaults to 300s.
	Timeout time.Duration
	// ConnectTimeout is the TCP connect (dial) timeout. Defaults to 30s.
	ConnectTimeout time.Duration
	HTTPClient     *http.Client
	Capabilities   *CapabilityRegistry
}

// AnthropicProvider implements Provider using Anthropic's /v1/messages SSE API.
type AnthropicProvider struct {
	cfg  AnthropicConfig
	http *http.Client
	caps *CapabilityRegistry
}

func NewAnthropic(cfg AnthropicConfig) (*AnthropicProvider, error) {
	if cfg.Model == "" {
		return nil, fmt.Errorf("llm.NewAnthropic: Model is empty")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.anthropic.com/v1"
	}
	cfg.BaseURL = NormalizeAnthropicBaseURL(cfg.BaseURL)
	cfg.APIKey = CleanAPIKey(cfg.APIKey)
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 4096
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 300 * time.Second // idle/inactivity timeout
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 30 * time.Second
	}
	if cfg.HTTPClient == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.DialContext = (&net.Dialer{Timeout: cfg.ConnectTimeout, KeepAlive: 30 * time.Second}).DialContext
		transport.ResponseHeaderTimeout = 0                 // bounded per request below so custom clients behave the same
		cfg.HTTPClient = &http.Client{Transport: transport} // no Client.Timeout: streaming body must not be capped
	}
	caps := cfg.Capabilities
	if caps == nil {
		caps = NewCapabilityRegistry()
	}
	return &AnthropicProvider{cfg: cfg, http: cfg.HTTPClient, caps: caps}, nil
}

func (p *AnthropicProvider) Name() string { return p.cfg.Model }

func (p *AnthropicProvider) SupportsVision() bool {
	if _, ok := p.caps.Get(p.cfg.Model); ok {
		return p.caps.HasVision(p.cfg.Model)
	}
	return strings.Contains(strings.ToLower(p.cfg.Model), "claude")
}

func (p *AnthropicProvider) Complete(ctx context.Context, msgs []Message, tools []ToolDef) (<-chan Delta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("Complete: no messages")
	}
	for i, m := range msgs {
		if err := m.Validate(); err != nil {
			return nil, fmt.Errorf("Complete: message %d: %w", i, err)
		}
	}
	// Do not gate attachments on catalog metadata. Anthropic-compatible
	// gateways frequently expose custom model IDs whose capabilities are not
	// present in our registry; the provider response is the source of truth.
	body, err := buildAnthropicRequest(p.cfg.Model, msgs, tools, true, p.cfg.MaxTokens)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	out := make(chan Delta, 16)
	go func() {
		defer close(out)
		defer func() {
			if r := recover(); r != nil {
				select {
				case out <- Delta{Err: fmt.Errorf("provider panic: %v", r)}:
				default:
				}
			}
		}()
		// Derive a cancellable child context so the idle watchdog can
		// abort a stalled stream. cancel runs after the read completes.
		reqCtx, cancel := context.WithCancel(ctx)
		notify := func(msg string) {
			select {
			case out <- Delta{Notice: msg}:
			case <-ctx.Done():
			}
		}
		resp, err := p.do(reqCtx, cancel, body, notify)
		if err != nil {
			cancel()
			select {
			case out <- Delta{Err: err}:
			case <-ctx.Done():
			}
			return
		}
		defer cancel()
		respBody := newIdleTimeoutReader(resp.Body, p.cfg.Timeout, cancel)
		defer respBody.Close()
		if err := p.streamSSE(ctx, respBody, out); err != nil {
			select {
			case out <- Delta{Err: err}:
			case <-ctx.Done():
			}
		}
	}()
	return out, nil
}

// do posts the request with the same bounded 429/5xx retry policy as
// the OpenAI provider: up to maxAttempts total, Retry-After honoured
// (clamped by rateLimitWaitBudget), a notify callback per wait so the
// UI shows the rate-limit pause. notify may be nil.
func (p *AnthropicProvider) do(ctx context.Context, cancel context.CancelFunc, body []byte, notify func(string)) (*http.Response, error) {
	const maxAttempts = 3
	waitBudget := rateLimitWaitBudget
	rateAttempts := 0
	thinkingRetried := false
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.BaseURL+"/messages", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("anthropic-version", anthropicVersion)
		if p.cfg.APIKey != "" {
			req.Header.Set("x-api-key", p.cfg.APIKey)
		}
		resp, err := doWithResponseHeaderTimeout(p.http, req, p.cfg.Timeout, cancel)
		if err != nil {
			return nil, fmt.Errorf("http: %w", err)
		}
		if resp.StatusCode/100 == 2 {
			return resp, nil
		}
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()
		if effort, ok := LearnReasoningEffortFromError(p.cfg.Model, resp.StatusCode, respBody); ok && !thinkingRetried {
			if patched, patchedOK := patchAnthropicThinking(body, effort); patchedOK {
				body = patched
				thinkingRetried = true
				continue
			}
		}
		if !isRetryableHTTPStatus(resp.StatusCode) {
			return nil, fmt.Errorf("http %d: %s%s%s", resp.StatusCode, string(respBody), badRequestEffortHint(resp.StatusCode, respBody), rateLimitExhaustedHint(p.cfg.Model, resp.StatusCode))
		}
		rateAttempts++
		if rateAttempts >= maxAttempts {
			return nil, fmt.Errorf("http %d: %s%s%s", resp.StatusCode, string(respBody), badRequestEffortHint(resp.StatusCode, respBody), rateLimitExhaustedHint(p.cfg.Model, resp.StatusCode))
		}
		backoff := retryWait(resp.Header, rateAttempts, waitBudget)
		waitBudget -= backoff
		if notify != nil {
			notify(rateLimitNotice(p.cfg.Model, resp.StatusCode, backoff, rateAttempts, maxAttempts))
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}
