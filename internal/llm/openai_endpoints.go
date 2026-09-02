package llm

import (
	"net/url"
	"strings"
)

// OpenAIEndpoints is the canonical endpoint set derived from a user supplied
// OpenAI-compatible URL. BaseURL is the API root, never the terminal
// /chat/completions endpoint.
type OpenAIEndpoints struct {
	BaseURL         string
	ChatCompletions string
	Models          string
}

// ResolveOpenAIEndpoints accepts the three forms users commonly paste:
//
//   - https://host.example
//   - https://host.example/v1
//   - https://host.example/v1/chat/completions
//   - https://host.example/v1/responses
//
// A bare host receives the conventional /v1 API root. Every explicit path is
// preserved as a custom gateway/deployment root; the resolver never inserts a
// version component inside a path supplied by the user.
func ResolveOpenAIEndpoints(raw string) OpenAIEndpoints {
	base := strings.TrimRight(strings.TrimSpace(raw), "/")
	if parsed, ok := resolveParsedOpenAIEndpoints(base); ok {
		return parsed
	}
	explicitTerminal := hasOpenAITerminalPath(base)
	base = trimOpenAITerminalPath(base, "/chat/completions")
	base = trimOpenAITerminalPath(base, "/responses")
	base = trimOpenAITerminalPath(base, "/models")

	if base != "" && !explicitTerminal && openAIEndpointNeedsV1(base) {
		base += "/v1"
	}
	return OpenAIEndpoints{
		BaseURL:         base,
		ChatCompletions: base + "/chat/completions",
		Models:          base + "/models",
	}
}

func resolveParsedOpenAIEndpoints(raw string) (OpenAIEndpoints, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return OpenAIEndpoints{}, false
	}
	u.Fragment = ""
	path := strings.TrimRight(u.Path, "/")
	explicitTerminal := hasOpenAITerminalPath(path)
	path = trimOpenAITerminalPath(path, "/chat/completions")
	path = trimOpenAITerminalPath(path, "/responses")
	path = trimOpenAITerminalPath(path, "/models")
	u.Path = path
	u.RawPath = ""
	if !explicitTerminal && openAIEndpointNeedsV1(u.String()) {
		u.Path = strings.TrimRight(u.Path, "/") + "/v1"
	}
	u.Path = strings.TrimRight(u.Path, "/")
	base := u.String()

	chatURL := *u
	chatURL.Path += "/chat/completions"
	modelsURL := *u
	modelsURL.Path += "/models"
	return OpenAIEndpoints{BaseURL: base, ChatCompletions: chatURL.String(), Models: modelsURL.String()}, true
}

func hasOpenAITerminalPath(value string) bool {
	value = strings.ToLower(strings.TrimRight(value, "/"))
	return strings.HasSuffix(value, "/chat/completions") || strings.HasSuffix(value, "/responses") || strings.HasSuffix(value, "/models")
}

func trimOpenAITerminalPath(base, suffix string) string {
	if strings.HasSuffix(strings.ToLower(base), suffix) {
		return strings.TrimRight(base[:len(base)-len(suffix)], "/")
	}
	return base
}

func openAIEndpointNeedsV1(base string) bool {
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		// Be conservative for malformed or scheme-less custom values: changing
		// their path would be more damaging than omitting the conventional /v1.
		return false
	}
	return strings.Trim(u.Path, "/") == ""
}
