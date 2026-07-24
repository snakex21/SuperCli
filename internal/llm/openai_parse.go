package llm

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"sort"
	"strings"
)

type llamaTimings struct {
	PromptN    int `json:"prompt_n"`
	CacheN     int `json:"cache_n"`
	PredictedN int `json:"predicted_n"`
}

// deriveCachedFromTimings fills Usage.CachedInput from llama.cpp's
// timings block when the OpenAI-style prompt_tokens_details breakdown
// is absent (older llama.cpp builds). Preference order: an explicit
// cache_n; else prompt_tokens - prompt_n (everything the server did
// not re-evaluate came from the KV cache). No-op when the usage
// already carries a cached count or there is nothing to derive, so
// cloud responses without timings are untouched.
func deriveCachedFromTimings(u *Usage, t *llamaTimings) {
	if u == nil || t == nil || u.CachedInput > 0 {
		return
	}
	cached := t.CacheN
	if cached == 0 && t.PromptN > 0 && u.Input > t.PromptN {
		cached = u.Input - t.PromptN
	}
	if cached > u.Input {
		cached = u.Input
	}
	if cached > 0 {
		u.CachedInput = cached
	}
}

type openaiRawChunk struct {
	Choices []struct {
		Delta map[string]json.RawMessage `json:"delta"`
	} `json:"choices"`
}

type openaiChoice struct {
	Index        int         `json:"index"`
	Delta        openaiDelta `json:"delta"`
	FinishReason string      `json:"finish_reason"`
}

type openaiDelta struct {
	Role             string          `json:"role,omitempty"`
	Content          string          `json:"content,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	ToolCalls        []openaiToolRef `json:"tool_calls,omitempty"`
}

func extractReasoningText(delta map[string]json.RawMessage) string {
	if len(delta) == 0 {
		return ""
	}
	// One delta, one reasoning text. Newer servers mirror the same
	// reasoning chunk under several keys (a structured `reasoning`
	// object plus a flat `reasoning_text`, say) — joining them doubled
	// every streamed word in the GUI. Pick a single best key instead:
	// well-known flat keys first, then any remaining reasoning-ish key
	// in deterministic (sorted) order.
	for _, key := range []string{"reasoning_content", "reasoning_text", "reasoning", "thinking", "thought"} {
		if raw, ok := delta[key]; ok {
			if s := extractStringLeaves(raw); strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	keys := make([]string, 0, len(delta))
	for key := range delta {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !isReasoningJSONKey(key) {
			continue
		}
		if s := extractStringLeaves(delta[key]); strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func isReasoningJSONKey(key string) bool {
	k := strings.ToLower(key)
	if strings.Contains(k, "finish") || strings.Contains(k, "token") || strings.Contains(k, "usage") {
		return false
	}
	return strings.Contains(k, "reasoning") || strings.Contains(k, "thinking") || strings.Contains(k, "thought")
}

func extractStringLeaves(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err == nil {
		var out []string
		for _, item := range arr {
			if v := extractStringLeaves(item); v != "" {
				out = append(out, v)
			}
		}
		return strings.Join(out, "")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err == nil {
		// Text-bearing keys first. When any of them yields text, that IS
		// the reasoning: the remaining keys are metadata (type, format,
		// index, …) and must never leak into the visible stream — that is
		// how literal "reasoning.text"/"unknown" ended up interleaved
		// with the model's thinking in the GUI.
		preferred := []string{"text", "content", "value", "delta", "thinking", "reasoning"}
		var out []string
		for _, key := range preferred {
			if v, ok := obj[key]; ok {
				if s := extractStringLeaves(v); s != "" {
					out = append(out, s)
				}
			}
		}
		if len(out) > 0 {
			return strings.Join(out, "")
		}
		// No known text key: walk the rest deterministically, skipping
		// anything metadata-shaped.
		keys := make([]string, 0, len(obj))
		for key := range obj {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if isReasoningMetadataKey(key) {
				continue
			}
			if s := extractStringLeaves(obj[key]); s != "" {
				out = append(out, s)
			}
		}
		return strings.Join(out, "")
	}
	return ""
}

// isReasoningMetadataKey reports whether a key inside a structured
// reasoning delta carries protocol metadata rather than model text.
func isReasoningMetadataKey(key string) bool {
	switch strings.ToLower(key) {
	case "type", "format", "index", "id", "status", "channel", "role", "name", "signature", "encrypted_content":
		return true
	}
	return false
}

type openaiToolRef struct {
	Index    int          `json:"index"`
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Function openaiToolFn `json:"function"`
}

type openaiToolFn struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type openaiUsage struct {
	PromptTokens            int                            `json:"prompt_tokens"`
	CompletionTokens        int                            `json:"completion_tokens"`
	TotalTokens             int                            `json:"total_tokens"`
	PromptTokensDetails     *openaiPromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *openaiCompletionTokensDetails `json:"completion_tokens_details,omitempty"`
}

// openaiPromptTokensDetails carries the cached-prompt breakdown that
// OpenAI and llama.cpp/LM Studio report inside usage. cached_tokens is
// the portion of prompt_tokens the backend served from its KV cache.
type openaiPromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

// openaiCompletionTokensDetails carries the reasoning-token breakdown
// that reasoning models report inside usage. reasoning_tokens counts the
// hidden chain-of-thought tokens billed as completion tokens.
type openaiCompletionTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

// EncodeBase64 is a small helper used by callers that need to
// turn a file into a base64 string for an ImageRef. It is a
// convenience around encoding/base64.
func EncodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// Ensure bufio is referenced (used in streaming helpers that may
// land here in a follow-up).
var _ = bufio.NewReader
