package llm

import (
	"context"
	"crypto/rand"
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
		PromptCacheKey:       responsesPromptCacheKey(baseURL, cfg.Model),
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

func responsesPromptCacheKey(baseURL, model string) string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		sum := sha256.Sum256([]byte(baseURL + "\x00" + model))
		copy(id[:], sum[:16])
	}
	// RFC 4122 UUID v4. The value is created once per provider instance and
	// reused across its turns, matching the thread-scoped Codex cache key.
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:16])
}

func (p *ResponsesProvider) Name() string { return p.inner.Name() }

func (p *ResponsesProvider) SupportsVision() bool { return p.inner.SupportsVision() }

func (p *ResponsesProvider) Complete(ctx context.Context, msgs []Message, tools []ToolDef) (<-chan Delta, error) {
	return p.inner.Complete(ctx, msgs, tools)
}
