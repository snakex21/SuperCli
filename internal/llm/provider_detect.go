package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// DetectProviderProtocol passively identifies the HTTP transport spoken by a
// custom provider. It never starts inference: only the models endpoint is
// queried. Generic/ambiguous model-list payloads default to OpenAI-compatible,
// which is the dominant local/proxy convention. Responses API cannot be
// distinguished from Chat Completions through /models alone, so it remains an
// explicit user choice unless the pasted URL itself ends in /responses.
func DetectProviderProtocol(ctx context.Context, baseURL, apiKey string) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "", fmt.Errorf("provider protocol detection: base URL is empty")
	}
	if typ := protocolFromTerminalPath(baseURL); typ != "" {
		return typ, nil
	}

	openaiURL := ResolveOpenAIEndpoints(baseURL).Models
	openaiBody, openaiStatus, openaiErr := probeModelsEndpoint(ctx, openaiURL, func(req *http.Request) {
		if key := CleanAPIKey(apiKey); key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		ApplyOpenCodeZenHeaders(req, baseURL)
	})
	if openaiErr == nil && openaiStatus >= 200 && openaiStatus < 300 {
		if typ := protocolFromModelsPayload(openaiBody); typ != "" {
			return typ, nil
		}
		return "openai", nil
	}

	anthropicBase := NormalizeAnthropicBaseURL(baseURL)
	anthropicURL := anthropicBase + "/models"
	if !strings.HasSuffix(anthropicBase, "/v1") {
		anthropicURL = anthropicBase + "/v1/models"
	}
	_, anthropicStatus, anthropicErr := probeModelsEndpoint(ctx, anthropicURL, func(req *http.Request) {
		req.Header.Set("anthropic-version", anthropicVersion)
		if key := CleanAPIKey(apiKey); key != "" {
			req.Header.Set("x-api-key", key)
		}
	})
	if anthropicErr == nil && anthropicStatus >= 200 && anthropicStatus < 300 {
		// If OpenAI-style auth failed while native Anthropic auth succeeded,
		// successful discovery is sufficient even for a sparse model payload.
		return "anthropic", nil
	}

	return "", fmt.Errorf(
		"provider protocol detection failed (openai models: %s; anthropic models: %s)",
		probeFailure(openaiStatus, openaiErr), probeFailure(anthropicStatus, anthropicErr),
	)
}

func protocolFromTerminalPath(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	path := strings.ToLower(strings.TrimRight(u.Path, "/"))
	switch {
	case strings.HasSuffix(path, "/messages"):
		return "anthropic"
	case strings.HasSuffix(path, "/responses"):
		return "responses"
	case strings.HasSuffix(path, "/chat/completions"):
		return "openai"
	default:
		return ""
	}
}

func probeModelsEndpoint(ctx context.Context, endpoint string, headers func(*http.Request)) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	if headers != nil {
		headers(req)
	}
	resp, err := (&http.Client{Timeout: ProviderDiscoveryTimeout}).Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

func protocolFromModelsPayload(body []byte) string {
	var payload struct {
		Object string `json:"object"`
		Data   []struct {
			Object      string `json:"object"`
			Type        string `json:"type"`
			OwnedBy     string `json:"owned_by"`
			DisplayName string `json:"display_name"`
			CreatedAt   string `json:"created_at"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return ""
	}
	if strings.EqualFold(payload.Object, "list") {
		return "openai"
	}
	for _, item := range payload.Data {
		if item.DisplayName != "" || item.CreatedAt != "" || strings.EqualFold(item.Type, "model") {
			return "anthropic"
		}
		if item.OwnedBy != "" || strings.EqualFold(item.Object, "model") {
			return "openai"
		}
	}
	return ""
}

func probeFailure(status int, err error) string {
	if err != nil {
		return err.Error()
	}
	if status != 0 {
		return fmt.Sprintf("HTTP %d", status)
	}
	return "no response"
}
