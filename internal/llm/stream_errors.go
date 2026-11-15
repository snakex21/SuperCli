package llm

import (
	"encoding/json"
	"strconv"
	"strings"
)

// openAIStreamError turns an OpenAI-compatible in-stream error object
// ({"error":{"message","type","code"}} or a bare string) into a typed
// ProviderStreamError, keeping the historical "provider stream error: …" text.
func openAIStreamError(raw json.RawMessage) error {
	e := &ProviderStreamError{Prefix: "provider stream error", Message: openAIErrorMessage(raw)}
	var object struct {
		Type   string `json:"type"`
		Code   any    `json:"code"`
		Status any    `json:"status"`
	}
	if json.Unmarshal(raw, &object) == nil {
		e.Type = object.Type
		e.Code = jsonScalarString(object.Code)
		if e.Code == "" {
			e.Code = jsonScalarString(object.Status)
		}
	}
	return e
}

// anthropicStreamError types an Anthropic "error" SSE event
// (overloaded_error, rate_limit_error, api_error, …).
func anthropicStreamError(errorType, message string) error {
	if message == "" {
		message = "stream error"
	}
	return &ProviderStreamError{Prefix: "anthropic", Type: errorType, Message: message}
}

// responsesStreamError types a Responses API error/response.failed event.
func responsesStreamError(apiName, code, message string) error {
	return &ProviderStreamError{Prefix: apiName, Code: code, Message: message}
}

func jsonScalarString(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case json.Number:
		return x.String()
	}
	return ""
}
