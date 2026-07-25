package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

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
		resp, err := (&http.Client{Timeout: ProviderDiscoveryTimeout}).Do(req)
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
	client := &http.Client{Timeout: ProviderDiscoveryTimeout}
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
