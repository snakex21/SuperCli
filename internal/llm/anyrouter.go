package llm

import (
	"net/url"
	"strings"
)

// IsAnyRouterBaseURL recognizes only the provider's HTTPS API origin and
// documented API roots. A lookalike host or custom proxy retains its own API.
func IsAnyRouterBaseURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.User != nil ||
		!strings.EqualFold(u.Hostname(), "anyrouter.top") || (u.Port() != "" && u.Port() != "443") || u.RawQuery != "" {
		return false
	}
	switch strings.TrimRight(u.Path, "/") {
	case "", "/v1", "/v1/models", "/v1/messages", "/v1/responses", "/v1/chat/completions":
		return true
	default:
		return false
	}
}

// NormalizeAnyRouterBaseURL accepts a site root or a pasted API endpoint.
// All three model transports use this same /v1 root.
func NormalizeAnyRouterBaseURL(raw string) string {
	if !IsAnyRouterBaseURL(raw) {
		return raw
	}
	u, _ := url.Parse(strings.TrimSpace(raw))
	u.Path, u.RawPath, u.Fragment = "/v1", "", ""
	return u.String()
}

// AnyRouterModelProtocol resolves this mixed catalog at construction time,
// without an inference probe or a prompt instruction. Unknown model families
// retain the explicitly configured protocol; a catalog entry is not a promise
// of current upstream availability, and models are never silently replaced.
func AnyRouterModelProtocol(baseURL, model string) string {
	if !IsAnyRouterBaseURL(baseURL) {
		return ""
	}
	model = strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(model, "claude-"), strings.HasSuffix(model, "-cc-format"):
		return "anthropic"
	case strings.HasPrefix(model, "gpt-5"), strings.HasPrefix(model, "gpt-6"),
		strings.HasPrefix(model, "codex-"), model == "o1", strings.HasPrefix(model, "o1-"),
		model == "o3", strings.HasPrefix(model, "o3-"), model == "o4", strings.HasPrefix(model, "o4-"):
		return "responses"
	case strings.HasPrefix(model, "gpt-"), strings.HasPrefix(model, "gemini-"), strings.HasPrefix(model, "chatgpt-"):
		return "openai"
	default:
		return ""
	}
}
