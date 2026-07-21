package llm

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
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

// providerListCache memoizes successful ListProviderModels calls keyed by
// protocol, baseURL, and a one-way credential fingerprint. Errors are never
// cached. Guarded by its own
// mutex; the map is tiny (one entry per configured provider).
var providerListCache = struct {
	mu sync.Mutex
	m  map[string]providerListEntry
}{m: make(map[string]providerListEntry)}

type providerListEntry struct {
	models  []ModelInfo
	fetched time.Time
}

func providerModelsCacheKey(protocol, baseURL, apiKey string) string {
	cleanBase := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	sum := sha256.Sum256([]byte(CleanAPIKey(apiKey)))
	return protocol + "\x00" + cleanBase + "\x00" + fmt.Sprintf("%x", sum[:8])
}

// InvalidateProviderModelCache forces the next discovery request for baseURL
// to hit the server. Provider edits call this before their verification scan
// so a success cached under old credentials cannot hide a 401.
func InvalidateProviderModelCache(baseURL string) {
	cleanBase := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	providerListCache.mu.Lock()
	defer providerListCache.mu.Unlock()
	for key := range providerListCache.m {
		if strings.HasPrefix(key, "openai\x00"+cleanBase+"\x00") || strings.HasPrefix(key, "anthropic\x00"+cleanBase+"\x00") {
			delete(providerListCache.m, key)
		}
	}
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
	models, err := ListProviderModelInfos(ctx, baseURL, apiKey)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids, nil
}

// ListProviderModelInfos returns provider-advertised models together with
// modality and context metadata when the endpoint publishes it. Missing
// metadata remains unknown; model names are never used to infer vision.
func ListProviderModelInfos(ctx context.Context, baseURL, apiKey string) ([]ModelInfo, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("llm: ListProviderModels: baseURL is empty")
	}
	cacheKey := providerModelsCacheKey("openai", baseURL, apiKey)
	providerListCache.mu.Lock()
	if e, ok := providerListCache.m[cacheKey]; ok && time.Since(e.fetched) < providerListCacheTTL {
		models := append([]ModelInfo(nil), e.models...)
		providerListCache.mu.Unlock()
		return models, nil
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
	out, err := parseProviderModelInfos(body)
	if err != nil {
		return nil, fmt.Errorf("llm: ListProviderModels: parse: %w", err)
	}
	providerListCache.mu.Lock()
	providerListCache.m[cacheKey] = providerListEntry{models: append([]ModelInfo(nil), out...), fetched: time.Now()}
	providerListCache.mu.Unlock()
	return out, nil
}

type providerModelWire struct {
	ID               string          `json:"id"`
	Key              string          `json:"key"`
	Type             string          `json:"type"`
	ContextLength    json.RawMessage `json:"context_length"`
	MaxContextLength json.RawMessage `json:"max_context_length"`
	ContextWindow    json.RawMessage `json:"context_window"`
	Capabilities     json.RawMessage `json:"capabilities"`
	InputModalities  []string        `json:"input_modalities"`
	Architecture     struct {
		InputModalities []string `json:"input_modalities"`
	} `json:"architecture"`
}

func parseProviderModelInfos(body []byte) ([]ModelInfo, error) {
	var payload struct {
		Data   []providerModelWire `json:"data"`
		Models []providerModelWire `json:"models"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	wires := payload.Data
	if len(wires) == 0 {
		wires = payload.Models
	}
	out := make([]ModelInfo, 0, len(wires))
	for _, wire := range wires {
		id := strings.TrimSpace(wire.ID)
		if id == "" {
			id = strings.TrimSpace(wire.Key)
		}
		if id == "" {
			continue
		}
		model := HeuristicCapabilities(id)
		model.ContextLength = firstPositiveInt(wire.ContextLength, wire.MaxContextLength, wire.ContextWindow)
		if modalities := firstNonEmptyStrings(wire.Architecture.InputModalities, wire.InputModalities); len(modalities) > 0 {
			model.VisionKnown = true
			model.Vision = containsFold(modalities, "image")
		}
		applyCapabilityMetadata(&model, wire.Capabilities)
		switch strings.ToLower(strings.TrimSpace(wire.Type)) {
		case "vlm":
			model.Vision, model.VisionKnown = true, true
		case "embedding", "embeddings":
			model.Vision, model.VisionKnown, model.ToolUse = false, true, false
		}
		out = append(out, model)
	}
	return out, nil
}

func applyCapabilityMetadata(model *ModelInfo, raw json.RawMessage) {
	if model == nil || len(raw) == 0 || string(raw) == "null" {
		return
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) == nil {
		if value, ok := object["vision"]; ok {
			var supported bool
			if json.Unmarshal(value, &supported) == nil {
				model.Vision, model.VisionKnown = supported, true
			}
		}
		if value, ok := object["trained_for_tool_use"]; ok {
			_ = json.Unmarshal(value, &model.ToolUse)
		}
		return
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		for _, capability := range list {
			if strings.EqualFold(capability, "vision") || strings.EqualFold(capability, "image") {
				model.Vision, model.VisionKnown = true, true
			}
			if strings.EqualFold(capability, "tool_use") || strings.EqualFold(capability, "tools") {
				model.ToolUse = true
			}
		}
	}
}

func firstPositiveInt(values ...json.RawMessage) int {
	for _, raw := range values {
		var n float64
		if len(raw) > 0 && json.Unmarshal(raw, &n) == nil && n > 0 {
			return int(n)
		}
	}
	return 0
}

func firstNonEmptyStrings(values ...[]string) []string {
	for _, value := range values {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}

func containsFold(values []string, wanted string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), wanted) {
			return true
		}
	}
	return false
}

// ListLocalNativeModelInfos queries native discovery endpoints for
// local/private OpenAI-compatible servers. The returned schema, not a model
// or configured provider name, determines whether the endpoint applies.
func ListLocalNativeModelInfos(ctx context.Context, baseURL, apiKey string) []ModelInfo {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || !isLocalDiscoveryHost(u.Hostname()) || !strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/v1") {
		return nil
	}
	root := u.Scheme + "://" + u.Host
	for _, path := range []string{"/api/v1/models", "/api/v0/models"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, root+path, nil)
		if err != nil {
			continue
		}
		if key := CleanAPIKey(apiKey); key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		resp, err := (&http.Client{Timeout: providerListTimeout}).Do(req)
		if err != nil {
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if readErr == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if models, parseErr := parseProviderModelInfos(body); parseErr == nil && len(models) > 0 {
				return models
			}
		}
	}
	return nil
}

func isLocalDiscoveryHost(host string) bool {
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	// Loopback IPs are deliberately not probed: localhost is the normal
	// desktop configuration, while 127.0.0.1 is also heavily used by generic
	// OpenAI-compatible test/proxy servers that need not expose native routes.
	return ip != nil && !ip.IsLoopback() && (ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

// ListAnthropicModels returns model ids from Anthropic's native /v1/models
// endpoint. Anthropic uses x-api-key + anthropic-version instead of Bearer auth.
func ListAnthropicModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("llm: ListAnthropicModels: baseURL is empty")
	}
	cacheKey := providerModelsCacheKey("anthropic", baseURL, apiKey)
	providerListCache.mu.Lock()
	if e, ok := providerListCache.m[cacheKey]; ok && time.Since(e.fetched) < providerListCacheTTL {
		ids := make([]string, 0, len(e.models))
		for _, model := range e.models {
			ids = append(ids, model.ID)
		}
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
	models := make([]ModelInfo, 0, len(out))
	for _, id := range out {
		models = append(models, HeuristicCapabilities(id))
	}
	providerListCache.m[cacheKey] = providerListEntry{models: models, fetched: time.Now()}
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
