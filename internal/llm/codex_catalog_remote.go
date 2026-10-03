package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"supercli/internal/buildinfo"
)

// CodexModel retains account-advertised metadata. It intentionally excludes
// upstream instructions: discovery must not replace SuperCli's agent prompt.
type CodexModel struct {
	Slug                     string                `json:"slug"`
	DisplayName              string                `json:"display_name,omitempty"`
	Description              string                `json:"description,omitempty"`
	Visibility               string                `json:"visibility"`
	ContextWindow            int                   `json:"context_window,omitempty"`
	MaxContextWindow         int                   `json:"max_context_window,omitempty"`
	InputModalities          []string              `json:"input_modalities"`
	DefaultReasoningLevel    string                `json:"default_reasoning_level,omitempty"`
	SupportedReasoningLevels []CodexReasoningLevel `json:"supported_reasoning_levels"`
}

type CodexReasoningLevel struct {
	Effort      string `json:"effort"`
	Description string `json:"description,omitempty"`
}

type CodexCatalogConfig struct {
	BackendURL string
	Tokens     CodexTokenSource
	HTTPClient *http.Client
	// ClientVersion is optional for compatibility with custom Codex backends.
	// The official Codex client passes its version in this query parameter.
	ClientVersion string
}

// ListCodexModels reads the selected account's catalog, never inference. It
// refreshes rejected OAuth credentials once and uses the refreshed account id
// on the second request. Errors omit response bodies and authentication data.
func ListCodexModels(ctx context.Context, cfg CodexCatalogConfig) ([]CodexModel, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cfg.Tokens == nil {
		return nil, fmt.Errorf("codex models: token source required")
	}
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(cfg.BackendURL), "/"))
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, fmt.Errorf("codex models: invalid backend URL")
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/models"
	// Native Codex discovery includes the client version, as the upstream
	// endpoint does. Custom backends may keep the query omitted.
	if cfg.ClientVersion == "" && strings.EqualFold(base.Hostname(), "chatgpt.com") {
		cfg.ClientVersion = strings.TrimPrefix(buildinfo.Version, "v")
	}
	if cfg.ClientVersion != "" {
		q := base.Query()
		q.Set("client_version", cfg.ClientVersion)
		base.RawQuery = q.Encode()
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: ProviderDiscoveryTimeout}
	}
	ctx, cancel := context.WithTimeout(ctx, ProviderDiscoveryTimeout)
	defer cancel()
	for attempt := 0; attempt < 2; attempt++ {
		access, accountID, err := cfg.Tokens.Token(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("codex models: credentials unavailable; log in again")
		}
		if strings.TrimSpace(access) == "" {
			return nil, fmt.Errorf("codex models: not logged in")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("codex models: invalid request")
		}
		req.Header.Set("Authorization", "Bearer "+access)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("originator", "codex_cli_go")
		if accountID != "" {
			req.Header.Set("ChatGPT-Account-Id", accountID)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("codex models: request failed: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			if _, err := cfg.Tokens.Refresh(ctx); err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, fmt.Errorf("codex models: credentials refresh failed; log in again")
			}
			continue
		}
		if resp.StatusCode/100 != 2 {
			return nil, fmt.Errorf("codex models: http %d", resp.StatusCode)
		}
		if readErr != nil {
			return nil, fmt.Errorf("codex models: response read failed: %w", readErr)
		}
		if len(body) > 4<<20 {
			return nil, fmt.Errorf("codex models: response exceeds 4 MiB")
		}
		var payload struct {
			Models *[]CodexModel `json:"models"`
		}
		if err := json.Unmarshal(body, &payload); err != nil || payload.Models == nil {
			return nil, fmt.Errorf("codex models: invalid models response")
		}
		if len(*payload.Models) > 4096 {
			return nil, fmt.Errorf("codex models: too many models")
		}
		models := make([]CodexModel, 0, len(*payload.Models))
		seen := make(map[string]bool)
		for _, model := range *payload.Models {
			model.Slug = strings.TrimSpace(model.Slug)
			if model.Visibility != "list" || model.Slug == "" || len(model.Slug) > 256 || seen[model.Slug] {
				continue
			}
			seen[model.Slug] = true
			models = append(models, model)
		}
		return models, nil
	}
	return nil, fmt.Errorf("codex models: credentials rejected")
}

// RegisterCodexModels registers only the account-advertised visible models.
// Account availability remains the caller's inventory, not a static ID table.
func RegisterCodexModels(r *CapabilityRegistry, providerName, backendURL string, models []CodexModel) []string {
	if r == nil {
		return nil
	}
	if providerName == "" {
		providerName = "codex"
	}
	ids := make([]string, 0, len(models))
	seen := make(map[string]bool)
	for _, model := range models {
		if model.Slug == "" || model.Visibility != "list" || seen[model.Slug] {
			continue
		}
		seen[model.Slug] = true
		info := ModelInfo{ID: model.Slug, Provider: providerName, Transport: ModelTransportResponses,
			ToolUse: true, Stream: true, Source: SourceProvider, LastVerified: time.Now(), Notes: model.Description}
		info.ContextLength = model.ContextWindow
		if info.ContextLength <= 0 {
			info.ContextLength = model.MaxContextWindow
		}
		if info.ContextLength < 0 {
			info.ContextLength = 0
		}
		info.VisionKnown = model.InputModalities != nil
		for _, modality := range model.InputModalities {
			if modality == "image" {
				info.Vision = true
			}
		}
		levels := make([]string, 0, len(model.SupportedReasoningLevels))
		for _, level := range model.SupportedReasoningLevels {
			levels = append(levels, level.Effort)
			if level.Effort != "none" && level.Effort != "" {
				info.Reasoning = true
			}
		}
		info.ReasoningKnown = model.SupportedReasoningLevels != nil
		if len(levels) > 0 {
			SetReasoningEffortSupport(model.Slug, levels)
			SetReasoningEffortSupport(ReasoningSupportKey(backendURL, model.Slug), levels)
		} else if info.ReasoningKnown {
			setReasoningEffortUnsupported(model.Slug)
			setReasoningEffortUnsupported(ReasoningSupportKey(backendURL, model.Slug))
		}
		if existing, ok := r.Get(model.Slug); ok {
			info.InputCost, info.OutputCost = existing.InputCost, existing.OutputCost
			if !info.VisionKnown {
				info.Vision, info.VisionKnown = existing.Vision, existing.VisionKnown
			}
			if !info.ReasoningKnown {
				info.Reasoning, info.ReasoningKnown = existing.Reasoning, existing.ReasoningKnown
			}
			if info.ContextLength == 0 {
				info.ContextLength = existing.ContextLength
			}
		}
		r.Register(info)
		ids = append(ids, model.Slug)
	}
	return ids
}
