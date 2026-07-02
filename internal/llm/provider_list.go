package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"
)

// providerListTimeout caps how long a /v1/models
// fetch may take. The endpoint is small (a few KB
// of JSON) so 10s is generous; we use the same
// value for probes to keep the constants tidy.
const providerListTimeout = 10 * time.Second

// providerListCacheTTL is how long a successful /v1/models
// response is reused before re-fetching. One hour: long enough
// that menu opens and rescans are free, short enough that a
// provider enabling/retiring models surfaces the same day.
const providerListCacheTTL = time.Hour

// providerListCache memoizes successful ListProviderModels calls
// keyed by baseURL. Errors are never cached. Guarded by its own
// mutex; the map is tiny (one entry per configured provider).
var providerListCache = struct {
	mu sync.Mutex
	m  map[string]providerListEntry
}{m: make(map[string]providerListEntry)}

type providerListEntry struct {
	ids     []string
	fetched time.Time
}

// ListProviderModels returns the model ids exposed
// by a provider's /v1/models endpoint. The result
// is what the provider advertises; the caller
// feeds each id into HeuristicCapabilities to get
// capability flags.
//
// baseURL is the API root (e.g.
// https://api.openai.com/v1). A trailing slash is
// tolerated. An empty baseURL is an error. A
// non-2xx response is an error (we do not return
// partial data — silently dropping a real failure
// would corrupt the catalog). A 2xx with empty
// data is OK and returns an empty slice.
//
// If apiKey is non-empty, it is sent as a Bearer
// token. Some local servers (llama.cpp, LM Studio)
// ignore the header; some reject missing auth.
// Callers can pass an empty key for the unauth
// case.
func ListProviderModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("llm: ListProviderModels: baseURL is empty")
	}
	providerListCache.mu.Lock()
	if e, ok := providerListCache.m[baseURL]; ok && time.Since(e.fetched) < providerListCacheTTL {
		ids := append([]string(nil), e.ids...)
		providerListCache.mu.Unlock()
		return ids, nil
	}
	providerListCache.mu.Unlock()
	base := strings.TrimRight(baseURL, "/")
	u := base + "/models"
	if !strings.HasSuffix(base, "/v1") && !strings.Contains(base, "api.kilo.ai") {
		u = base + "/v1/models"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("llm: ListProviderModels: %w", err)
	}
	if key := CleanAPIKey(apiKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{Timeout: providerListTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llm: ListProviderModels: %w", err)
	}
	defer resp.Body.Close()
	// Cap the body at 4 MB. Real /v1/models
	// responses are a few KB; 4 MB covers the
	// wildest local llama.cpp catalogs.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("llm: ListProviderModels: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("llm: ListProviderModels: status %d: %s", resp.StatusCode, body)
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("llm: ListProviderModels: parse: %w", err)
	}
	out := make([]string, 0, len(payload.Data))
	for _, m := range payload.Data {
		if m.ID != "" {
			out = append(out, m.ID)
		}
	}
	providerListCache.mu.Lock()
	providerListCache.m[baseURL] = providerListEntry{ids: append([]string(nil), out...), fetched: time.Now()}
	providerListCache.mu.Unlock()
	return out, nil
}

// ListAnthropicModels returns model ids from Anthropic's native /v1/models
// endpoint. Anthropic uses x-api-key + anthropic-version instead of Bearer auth.
func ListAnthropicModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("llm: ListAnthropicModels: baseURL is empty")
	}
	cacheKey := "anthropic:" + baseURL
	providerListCache.mu.Lock()
	if e, ok := providerListCache.m[cacheKey]; ok && time.Since(e.fetched) < providerListCacheTTL {
		ids := append([]string(nil), e.ids...)
		providerListCache.mu.Unlock()
		return ids, nil
	}
	providerListCache.mu.Unlock()
	base := NormalizeAnthropicBaseURL(baseURL)
	u := base + "/models"
	if !strings.HasSuffix(base, "/v1") {
		u = base + "/v1/models"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("llm: ListAnthropicModels: %w", err)
	}
	req.Header.Set("anthropic-version", anthropicVersion)
	if key := CleanAPIKey(apiKey); key != "" {
		req.Header.Set("x-api-key", key)
	}
	client := &http.Client{Timeout: providerListTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llm: ListAnthropicModels: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("llm: ListAnthropicModels: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("llm: ListAnthropicModels: status %d: %s", resp.StatusCode, body)
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("llm: ListAnthropicModels: parse: %w", err)
	}
	out := make([]string, 0, len(payload.Data))
	for _, m := range payload.Data {
		if m.ID != "" {
			out = append(out, m.ID)
		}
	}
	providerListCache.mu.Lock()
	providerListCache.m[cacheKey] = providerListEntry{ids: append([]string(nil), out...), fetched: time.Now()}
	providerListCache.mu.Unlock()
	return out, nil
}

// ListProviderModelContexts fetches /v1/models and returns a
// model-id → context-window map for entries that advertise one.
// Different servers use different field names (OpenRouter:
// context_length, LM Studio: max_context_length, others:
// context_window); all are parsed defensively — anything
// missing or malformed is simply skipped. Models without
// metadata are absent from the map.
func ListProviderModelContexts(ctx context.Context, baseURL, apiKey string) (map[string]int, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("llm: ListProviderModelContexts: baseURL is empty")
	}
	base := strings.TrimRight(baseURL, "/")
	u := base + "/models"
	if !strings.HasSuffix(base, "/v1") && !strings.Contains(base, "api.kilo.ai") {
		u = base + "/v1/models"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("llm: ListProviderModelContexts: %w", err)
	}
	if key := CleanAPIKey(apiKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{Timeout: providerListTimeout}
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
		if strings.HasPrefix(strings.ToLower(s), "bearer ") {
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

// visionMarkers and reasoningMarkers and
// nonToolMarkers are the substring lists used by
// HeuristicCapabilities. The lists are kept
// package-private to make it easy to extend them
// as new naming conventions appear.
var (
	visionMarkers    = []string{"vision"}
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
//   - "vision" anywhere → Vision=true.
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
	if containsAny(lower, visionMarkers) {
		m.Vision = true
	}
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

// ListFreeModels fetches /v1/models and returns only models whose
// cost.input is 0 (free tier). Used by providers like OpenCode Zen
// where the public API key gives access to both free and paid
// models, but we want to surface only the free ones.
func ListFreeModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	base := strings.TrimRight(baseURL, "/")
	u := base + "/models"
	if !strings.HasSuffix(base, "/v1") && !strings.Contains(base, "api.kilo.ai") && !strings.Contains(base, "opencode.ai/zen") {
		u = base + "/v1/models"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("llm: ListFreeModels: %w", err)
	}
	if key := CleanAPIKey(apiKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{Timeout: providerListTimeout}
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
			ID   string `json:"id"`
			Cost *struct {
				Input float64 `json:"input"`
			} `json:"cost"`
			Pricing *struct {
				Input float64 `json:"input"`
			} `json:"pricing"`
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
		inputCost := -1.0
		if m.Cost != nil {
			inputCost = m.Cost.Input
		} else if m.Pricing != nil {
			inputCost = m.Pricing.Input
		}
		if inputCost == 0 {
			out = append(out, m.ID)
		}
	}
	return out, nil
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
