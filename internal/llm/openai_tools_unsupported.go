package llm

import (
	"net/http"
	"strings"
	"sync"
)

// Many OpenAI-compatible hosts serve models that cannot call functions —
// most Cloudflare Workers AI text models, small local GGUFs behind a proxy,
// some reseller routes. Sent a `tools` array they answer 400/422 ("does not
// support tools", Workers AI schema errors on '/tools', "Unrecognized request
// argument supplied: tools"). Instead of failing every agent turn with that
// cryptic body, the transport retries the request once without tools, tells
// the user why in a notice, and remembers the endpoint+model for the life of
// the process so later requests skip the doomed attempt.
var toolsUnsupportedEndpoints sync.Map

var toolsUnsupportedPhrases = []string{
	"does not support tools", "doesn't support tools", "do not support tools", "not support tool",
	"tools are not supported", "tools is not supported", "tool use is not supported",
	"tool calling is not supported", "tool calls are not supported", "tool_choice is not supported",
	"function calling is not supported", "does not support function calling",
	"doesn't support function calling", "function calling not supported", "tools not supported",
	"unrecognized request argument supplied: tools", "unsupported parameter: 'tools'",
	"unsupported parameter: tools", "'/tools'", "'/tools/", "'/tool_choice'", "\"tools\" is not allowed",
	"extra inputs are not permitted", // pydantic-style servers rejecting unknown `tools`
}

// isToolsUnsupportedError reports a client error that blames the tools
// field itself. It must stay narrow: a 400 about a malformed tool CALL in the
// history ("invalid tool_call_id") is a different problem and is not matched.
func isToolsUnsupportedError(status int, body []byte) bool {
	if status != http.StatusBadRequest && status != http.StatusUnprocessableEntity &&
		status != http.StatusNotImplemented && status != http.StatusInternalServerError {
		return false
	}
	text := strings.ToLower(string(body))
	if strings.Contains(text, "extra inputs are not permitted") && !strings.Contains(text, "tools") {
		return false
	}
	for _, phrase := range toolsUnsupportedPhrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

func (p *OpenAIProvider) noteToolsUnsupported() {
	toolsUnsupportedEndpoints.Store(toolsEffortEndpointKey(p.cfg.BaseURL, p.cfg.Model), struct{}{})
}

func (p *OpenAIProvider) toolsKnownUnsupported() bool {
	_, ok := toolsUnsupportedEndpoints.Load(toolsEffortEndpointKey(p.cfg.BaseURL, p.cfg.Model))
	return ok
}

// resetToolsUnsupported clears the process-wide memory (tests only).
func resetToolsUnsupported() {
	toolsUnsupportedEndpoints.Range(func(k, _ any) bool {
		toolsUnsupportedEndpoints.Delete(k)
		return true
	})
}

// toolsUnsupportedNotice is the user-visible explanation. The web GUI and
// TUI localize it by pattern.
func toolsUnsupportedNotice(model string) string {
	return "model " + quoteModel(model) + " does not support tool calling on this endpoint; continuing without tools (answers only) — pick a function-calling model for agent work"
}

func quoteModel(model string) string { return "\"" + model + "\"" }
