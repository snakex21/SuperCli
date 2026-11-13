package llm

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ResponsesConfig configures an API-key authenticated OpenAI Responses API
// endpoint. It is intentionally separate from CodexConfig: both transports
// share the same wire schema, while Codex uses ChatGPT OAuth and additional
// backend-only headers.
type ResponsesConfig struct {
	BaseURL        string
	APIKey         string
	Model          string
	Timeout        time.Duration
	ConnectTimeout time.Duration
	HTTPClient     *http.Client
	Capabilities   *CapabilityRegistry
}

// ResponsesProvider adapts SuperCli messages and tool calls to the standard
// OpenAI Responses API while keeping ChatGPT subscription concerns out of the
// public provider surface.
type ResponsesProvider struct {
	inner *CodexProvider
}

type staticResponsesTokenSource struct {
	token string
}

func (s staticResponsesTokenSource) Token(context.Context) (string, string, error) {
	return s.token, "", nil
}

func (s staticResponsesTokenSource) Refresh(context.Context) (string, error) {
	return s.token, nil
}

// NewResponses builds a provider for POST <base>/responses. APIKey may be
// empty for local gateways that do not require authentication.
func NewResponses(cfg ResponsesConfig) (*ResponsesProvider, error) {
	// Accept either an API root or the full .../responses endpoint pasted from
	// provider documentation. Unlike Chat Completions, a bare custom host must
	// stay bare (some gateways intentionally serve POST /responses rather than
	// /v1/responses), so normalization only removes the terminal path.
	baseURL := normalizeResponsesBaseURL(cfg.BaseURL)
	inner, err := NewCodex(CodexConfig{
		BackendURL:           baseURL,
		Model:                cfg.Model,
		Tokens:               staticResponsesTokenSource{token: CleanAPIKey(cfg.APIKey)},
		Timeout:              cfg.Timeout,
		ConnectTimeout:       cfg.ConnectTimeout,
		HTTPClient:           cfg.HTTPClient,
		Capabilities:         cfg.Capabilities,
		StandardResponsesAPI: true,
		PromptCacheKey:       responsesPromptCacheKey(CleanAPIKey(cfg.APIKey), baseURL, cfg.Model),
	})
	if err != nil {
		return nil, err
	}
	return &ResponsesProvider{inner: inner}, nil
}

func normalizeResponsesBaseURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" && u.Host != "" {
		u.Fragment = ""
		path := strings.TrimRight(u.Path, "/")
		path = trimOpenAITerminalPath(path, "/responses")
		u.Path = strings.TrimRight(path, "/")
		u.RawPath = ""
		return strings.TrimRight(u.String(), "/")
	}
	return strings.TrimRight(trimOpenAITerminalPath(strings.TrimRight(raw, "/"), "/responses"), "/")
}

// responsesPromptCacheKey is the routing hint for one account, endpoint and
// model. It is deliberately NOT per conversation or per provider instance:
// OpenAI routes by key + prompt prefix, and every SuperCli conversation shares
// the same long prefix (instructions + tool definitions). Live on
// api.openai.com (gpt-5.4-nano, identical ~2.3k-token prefix, new request
// text each call) a fresh key per conversation reused the cached prefix on
// 3 of 9 first requests; one stable key reused it on 4 of 4 after the first.
// The API key only salts the hash so different accounts never share a routing
// key; the value is one-way and never contains the credential.
func responsesPromptCacheKey(apiKey, baseURL, model string) string {
	sum := sha256.Sum256([]byte("supercli/responses-cache\x00" + apiKey + "\x00" + baseURL + "\x00" + model))
	id := sum[:16]
	// RFC 4122 shaped (version 4 bits) for backends that validate the format.
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:16])
}

func (p *ResponsesProvider) Name() string { return p.inner.Name() }

func (p *ResponsesProvider) SupportsVision() bool { return p.inner.SupportsVision() }

func (p *ResponsesProvider) Complete(ctx context.Context, msgs []Message, tools []ToolDef) (<-chan Delta, error) {
	return p.inner.Complete(ctx, msgs, tools)
}
