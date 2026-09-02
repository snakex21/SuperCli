package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode"
)

func ListProviderModelContexts(ctx context.Context, baseURL, apiKey string) (map[string]int, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("llm: ListProviderModelContexts: baseURL is empty")
	}
	u := ResolveOpenAIEndpoints(baseURL).Models
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("llm: ListProviderModelContexts: %w", err)
	}
	if key := CleanAPIKey(apiKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	ApplyOpenCodeZenHeaders(req, baseURL)
	client := &http.Client{Timeout: ProviderDiscoveryTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llm: ListProviderModelContexts: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("llm: ListProviderModelContexts: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("llm: ListProviderModelContexts: status %d", resp.StatusCode)
	}
	var payload struct {
		Data []struct {
			ID               string          `json:"id"`
			ContextLength    json.RawMessage `json:"context_length"`
			MaxContextLength json.RawMessage `json:"max_context_length"`
			ContextWindow    json.RawMessage `json:"context_window"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("llm: ListProviderModelContexts: parse: %w", err)
	}
	out := make(map[string]int)
	for _, m := range payload.Data {
		if m.ID == "" {
			continue
		}
		for _, raw := range []json.RawMessage{m.ContextLength, m.MaxContextLength, m.ContextWindow} {
			if len(raw) == 0 {
				continue
			}
			var n float64 // tolerate numbers serialized as floats
			if err := json.Unmarshal(raw, &n); err == nil && n > 0 {
				out[m.ID] = int(n)
				break
			}
		}
	}
	return out, nil
}

// CleanAPIKey makes a pasted API key safe for HTTP headers.
// It removes surrounding whitespace and any control characters
// commonly introduced by terminal paste (CR/LF/TAB/NUL). API
// tokens should not contain whitespace, so this also removes
// accidental spaces copied before/after line wraps.
func CleanAPIKey(s string) string {
	s = strings.TrimSpace(s)
	for {
		before := s
		if len(s) >= 4 && strings.HasPrefix(s, `\"`) && strings.HasSuffix(s, `\"`) {
			s = strings.TrimSpace(s[2 : len(s)-2])
		}
		if len(s) >= 2 {
			first := s[0]
			last := s[len(s)-1]
			if (first == '"' && last == '"') || (first == '\'' && last == '\'') || (first == '`' && last == '`') {
				s = strings.TrimSpace(s[1 : len(s)-1])
			}
		}
		if strings.EqualFold(s, "bearer") {
			s = ""
		} else if strings.HasPrefix(strings.ToLower(s), "bearer ") {
			s = strings.TrimSpace(s[len("bearer "):])
		}
		if s == before {
			break
		}
	}
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// reasoningMarkers and nonToolMarkers are the substring lists used by
// HeuristicCapabilities. The lists are kept
// package-private to make it easy to extend them
// as new naming conventions appear.
var (
	reasoningMarkers = []string{
		"o1", "o3", "o4",
		"reasoning", "thinking",
		"r1", "qwq", "deepseek-r1",
	}
	nonToolMarkers = []string{
		"embed", "dall-e", "tts", "whisper",
	}
)

// HeuristicCapabilities inspects a model id and
// infers capability flags from well-known naming
// conventions. It is the fallback used after
// /v1/models returns an id we have not seen
// before — we do not probe every model on every
// run, so a cheap name-based guess beats a network
// round-trip.
//
// The rules (case-insensitive substring match):
//
//   - a known multimodal family marker (including Qwen 3.5) → Vision=true.
//   - any reasoning marker (o1/o3/o4, reasoning,
//     thinking, r1, qwq, deepseek-r1) → Reasoning
//     =true.
//   - any non-tool marker (embed, dall-e, tts,
//     whisper) → ToolUse=false. ToolUse defaults
//     to true so unknown models retain function
//     calling; we only turn it off when the id
//     clearly says "not a chat model".
//
// Stream is always set to true (every modern
// OpenAI-compat model streams). The Provider
// field is left empty — main.go sets it from the
// calling config because the heuristic cannot
// tell "anthropic" from "openrouter" from the id
// alone.
//
// The Source is SourceProvider because this
// function is always called as a consequence of
// a ListProviderModels hit.
func HeuristicCapabilities(id string) ModelInfo {
	m := ModelInfo{
		ID:      id,
		ToolUse: true,
		Stream:  true,
		Source:  SourceProvider,
	}
	lower := strings.ToLower(id)
	if containsAny(lower, reasoningMarkers) {
		m.Reasoning = true
	}
	if containsAny(lower, nonToolMarkers) {
		m.ToolUse = false
	}
	return m
}

// IsFreeModelID reports whether a provider model id is explicitly
// labelled as free. OpenCode/Kilo gateways can return paid models
// without pricing metadata, so the UI must not treat missing cost as
// free. We only accept a standalone "free" segment, e.g.
// "kilo-auto/free", "openai/gpt-oss-20b:free", or
// "deepseek-v4-flash-free".
func IsFreeModelID(id string) bool {
	for _, part := range strings.FieldsFunc(strings.ToLower(id), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if part == "free" {
			return true
		}
	}
	return false
}

func containsAny(haystack string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}

// ListFreeModels fetches /v1/models and returns only models explicitly marked
// free by the provider, labelled "free" in their ID, or carrying complete
// zero input/output pricing metadata. An explicit isFree=false wins over price
// heuristics because some gateways publish zero-priced non-chat previews.
func ListFreeModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	u := ResolveOpenAIEndpoints(baseURL).Models
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("llm: ListFreeModels: %w", err)
	}
	if key := CleanAPIKey(apiKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	ApplyOpenCodeZenHeaders(req, baseURL)
	client := &http.Client{Timeout: ProviderDiscoveryTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llm: ListFreeModels: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("llm: ListFreeModels: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("llm: ListFreeModels: status %d: %s", resp.StatusCode, body)
	}
	var payload struct {
		Data []struct {
			ID      string                     `json:"id"`
			IsFree  *bool                      `json:"isFree"`
			Cost    map[string]json.RawMessage `json:"cost"`
			Pricing map[string]json.RawMessage `json:"pricing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("llm: ListFreeModels: parse: %w", err)
	}
	var out []string
	for _, m := range payload.Data {
		if m.ID == "" {
			continue
		}
		// Kilo exposes the authoritative isFree flag and serializes prices as
		// strings. OpenCode currently exposes only IDs, whose free variants are
		// explicitly labelled. Accept zero pricing as a third, portable signal,
		// but only when both input and output fields are actually present: absent
		// JSON fields also decode as zero and must not make every model look free.
		free := IsFreeModelID(m.ID)
		if m.IsFree != nil {
			// When the provider supplies an explicit flag it outranks price
			// heuristics. Kilo has zero-priced non-chat preview entries which are
			// deliberately marked isFree=false and must not enter the chat picker.
			free = free || *m.IsFree
		} else {
			free = free || modelPricingIsZero(m.Cost) || modelPricingIsZero(m.Pricing)
		}
		if free {
			out = append(out, m.ID)
		}
	}
	return out, nil
}

func modelPricingIsZero(fields map[string]json.RawMessage) bool {
	if len(fields) == 0 {
		return false
	}
	input, inputOK := firstJSONPrice(fields, "input", "prompt")
	output, outputOK := firstJSONPrice(fields, "output", "completion")
	return inputOK && outputOK && input == 0 && output == 0
}

func firstJSONPrice(fields map[string]json.RawMessage, names ...string) (float64, bool) {
	for _, name := range names {
		raw, ok := fields[name]
		if !ok || len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var number float64
		if err := json.Unmarshal(raw, &number); err == nil {
			return number, true
		}
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			continue
		}
		parsed, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
		if err == nil {
			return parsed, true
		}
	}
	return 0, false
}

// KiloDefaultKey returns "anonymous" when the base URL points
// to Kilo AI, or "public" for OpenCode Zen, when no explicit
// API key was provided. Both services offer free-tier access
// without authentication.
func KiloDefaultKey(baseURL, apiKey string) string {
	if apiKey != "" {
		return apiKey
	}
	if strings.Contains(baseURL, "api.kilo.ai") {
		return "anonymous"
	}
	if strings.Contains(baseURL, "opencode.ai/zen") {
		return "public"
	}
	return apiKey
}
