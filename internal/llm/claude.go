package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"supercli/internal/account/claudeauth"
)

type AnthropicTokenSource interface {
	AccessToken(context.Context, bool) (string, error)
}

// NewClaude uses the native Messages transport with portable OAuth credentials.
// OAuth credentials must never be sent to a configurable proxy or gateway.
func NewClaude(cfg AnthropicConfig, dataDir string) (*AnthropicProvider, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("Claude requires the SuperCli data directory")
	}
	if cfg.BaseURL != "" && strings.TrimRight(cfg.BaseURL, "/") != claudeauth.BaseURL {
		return nil, fmt.Errorf("Claude account login requires %s", claudeauth.BaseURL)
	}
	mgr := claudeauth.NewManager(dataDir, claudeauth.Options{})
	if !mgr.LoggedIn() {
		return nil, fmt.Errorf("Claude: not logged in; use /login claude")
	}
	cfg.BaseURL, cfg.APIKey, cfg.Tokens = claudeauth.BaseURL, "", mgr
	return NewAnthropic(cfg)
}

func applyAnthropicOAuth(ctx context.Context, req *http.Request, tokens AnthropicTokenSource, force bool) error {
	token, err := tokens.AccessToken(ctx, force)
	if err != nil {
		return err
	}
	req.Header.Del("x-api-key")
	req.Header.Set("Authorization", "Bearer "+token)
	beta := req.Header.Get("anthropic-beta")
	for _, feature := range []string{"claude-code-20250219", claudeauth.Beta} {
		if !strings.Contains(","+beta+",", ","+feature+",") {
			if beta != "" {
				beta += ","
			}
			beta += feature
		}
	}
	req.Header.Set("anthropic-beta", beta)
	return nil
}

// The subscription Messages endpoint requires this compatibility preamble and
// a namespace for custom tools. Keep both at the wire boundary: SuperCli's
// instructions, tool dispatcher and saved history retain their own identities.
const claudeOAuthPreamble = "You are Claude Code, Anthropic's official CLI for Claude."

func claudeOAuthToolName(name string, encode bool) string {
	if encode {
		return "custom_" + name
	}
	return strings.TrimPrefix(name, "custom_")
}

func claudeOAuthRequest(body []byte, encode bool) ([]byte, error) {
	var req map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber() // tool arguments may contain integers larger than 2^53
	if err := decoder.Decode(&req); err != nil {
		return nil, err
	}
	var system []any
	switch value := req["system"].(type) {
	case string:
		if value != "" {
			system = []any{map[string]any{"type": "text", "text": value}}
		}
	case []any:
		system = value
	}
	if encode {
		system = append([]any{map[string]any{"type": "text", "text": claudeOAuthPreamble}}, system...)
	} else if len(system) > 0 {
		if block, ok := system[0].(map[string]any); ok && block["text"] == claudeOAuthPreamble {
			system = system[1:]
		}
	}
	if len(system) == 0 {
		delete(req, "system")
	} else {
		req["system"] = system
	}
	rename := func(value any) {
		if block, ok := value.(map[string]any); ok {
			if name, ok := block["name"].(string); ok {
				block["name"] = claudeOAuthToolName(name, encode)
			}
		}
	}
	if tools, ok := req["tools"].([]any); ok {
		for _, tool := range tools {
			rename(tool)
		}
	}
	if choice, ok := req["tool_choice"]; ok {
		rename(choice)
	}
	if messages, ok := req["messages"].([]any); ok {
		for _, value := range messages {
			message, _ := value.(map[string]any)
			if content, ok := message["content"].([]any); ok {
				for _, value := range content {
					if block, ok := value.(map[string]any); ok && block["type"] == "tool_use" {
						rename(block)
					}
				}
			}
		}
	}
	return json.Marshal(req)
}

// ListClaudeModels fetches the authenticated native inventory, including all
// pages. It never substitutes guessed model IDs after an upstream rejection.
func ListClaudeModels(ctx context.Context, dataDir string) ([]string, error) {
	return listClaudeModels(ctx, claudeauth.BaseURL, claudeauth.NewManager(dataDir, claudeauth.Options{}), &http.Client{Timeout: ProviderDiscoveryTimeout})
}

func listClaudeModels(ctx context.Context, base string, tokens AnthropicTokenSource, client *http.Client) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, ProviderDiscoveryTimeout)
	defer cancel()
	var ids []string
	seen := make(map[string]bool)
	cursor := ""
	authRetried := false
	for {
		endpoint := strings.TrimRight(base, "/") + "/models?limit=100"
		if cursor != "" {
			endpoint += "&after_id=" + url.QueryEscape(cursor)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("anthropic-version", anthropicVersion)
		if err := applyAnthropicOAuth(ctx, req, tokens, false); err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("Claude model discovery unavailable: %w", err)
		}
		b, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized && !authRetried {
			if _, err := tokens.AccessToken(ctx, true); err != nil {
				return nil, err
			}
			authRetried = true
			continue
		}
		if resp.StatusCode/100 != 2 {
			return nil, fmt.Errorf("Claude model discovery rejected (HTTP %d)", resp.StatusCode)
		}
		if readErr != nil {
			return nil, readErr
		}
		var page struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		if err := json.Unmarshal(b, &page); err != nil {
			return nil, fmt.Errorf("Claude: invalid models response")
		}
		for _, model := range page.Data {
			if model.ID != "" && !seen[model.ID] {
				ids = append(ids, model.ID)
				seen[model.ID] = true
			}
		}
		if !page.HasMore {
			return ids, nil
		}
		if page.LastID == "" || page.LastID == cursor || len(page.Data) == 0 {
			return nil, fmt.Errorf("Claude: invalid models pagination")
		}
		cursor = page.LastID
	}
}
