package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Hosted search is an optional enhancement: a provider's own web search
// tool (OpenAI Responses "web_search", xAI's compatible Responses API)
// answers web_search/web_lookup queries, and any failure or empty answer
// falls back to the local engines. The model sees the same tool and the same
// result format either way; only the "engine:" label differs.
//
// It is OFF by default ([web_search] hosted = "off"): every hosted call is
// billed (a per-call search fee plus tokens) and sends the query to the
// provider, while the keyless local engines already work with any model,
// including local ones. Users opt in with hosted = "openai", "xai" or
// "auto" (the first provider with a key).

// HostedSearchConfig selects a provider-hosted search backend.
type HostedSearchConfig struct {
	Provider string // "openai" or "xai"
	Model    string // small model that runs the hosted search tool
	APIKey   string
	BaseURL  string // Responses API base; defaults per provider
}

const hostedSearchTimeout = 25 * time.Second

var hostedDefaults = map[string]struct{ baseURL, model string }{
	"openai": {"https://api.openai.com/v1", "gpt-5.4-nano"},
	"xai":    {"https://api.x.ai/v1", "grok-4-fast"},
}

// WithHosted enables provider-hosted search. An unknown provider or a
// missing key leaves the tool on its local engines.
func (t *WebSearch) WithHosted(cfg HostedSearchConfig) *WebSearch {
	cfg.Provider = strings.ToLower(strings.TrimSpace(cfg.Provider))
	defaults, ok := hostedDefaults[cfg.Provider]
	if !ok || strings.TrimSpace(cfg.APIKey) == "" {
		t.hosted = nil
		return t
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaults.baseURL
	}
	if cfg.Model == "" {
		cfg.Model = defaults.model
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	t.hosted = &cfg
	return t
}

// HostedEngine reports the active hosted engine label ("" when off).
func (t *WebSearch) HostedEngine() string {
	if t.hosted == nil {
		return ""
	}
	return t.hosted.Provider + "-hosted"
}

func (t *WebSearch) searchHosted(ctx context.Context, a webSearchArgs) ([]WebSearchResult, error) {
	h := t.hosted
	tool := map[string]any{"type": "web_search"}
	if h.Provider == "openai" && len(a.IncludeDomains) > 0 {
		tool["filters"] = map[string]any{"allowed_domains": a.IncludeDomains}
	}
	prompt := "Search the web and list the most relevant pages for this query, one per line: title, then a one-sentence factual summary. Cite every page. Query: " + a.Query
	if a.Freshness != "" {
		prompt += " (only results from the past " + a.Freshness + ")"
	}
	body := map[string]any{
		"model":             h.Model,
		"input":             prompt,
		"tools":             []any{tool},
		"tool_choice":       "required",
		"include":           []string{"web_search_call.action.sources"},
		"max_output_tokens": 900,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	endpoint := h.BaseURL + "/responses"
	host := endpoint
	if u, err := url.Parse(endpoint); err == nil {
		host = u.Host
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.APIKey)
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, requestFailedErr(err, host)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, httpFailedErr(resp, host)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, webSearchMaxBody))
	if err != nil {
		return nil, err
	}
	return parseHostedResponse(raw, a.MaxResults)
}

// parseHostedResponse turns a Responses API answer into search hits: cited
// pages first (title plus the cited sentence as snippet), then any other
// consulted sources.
func parseHostedResponse(raw []byte, limit int) ([]WebSearchResult, error) {
	var parsed struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Output []struct {
			Type   string `json:"type"`
			Action struct {
				Sources []struct {
					URL   string `json:"url"`
					Title string `json:"title"`
				} `json:"sources"`
			} `json:"action"`
			Content []struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Annotations []struct {
					Type       string `json:"type"`
					URL        string `json:"url"`
					Title      string `json:"title"`
					StartIndex int    `json:"start_index"`
					EndIndex   int    `json:"end_index"`
				} `json:"annotations"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("hosted search: decode response: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return nil, fmt.Errorf("hosted search: %s", parsed.Error.Message)
	}
	var out []WebSearchResult
	seen := map[string]bool{}
	add := func(title, rawURL, snippet string) {
		u := strings.TrimSpace(rawURL)
		if u == "" || len(out) >= limit {
			return
		}
		// Providers tag cited links with tracking parameters.
		if parsedURL, err := url.Parse(u); err == nil {
			q := parsedURL.Query()
			q.Del("utm_source")
			parsedURL.RawQuery = q.Encode()
			u = parsedURL.String()
		}
		key := strings.TrimSuffix(u, "/")
		if seen[key] {
			return
		}
		seen[key] = true
		if title == "" {
			if parsedURL, err := url.Parse(u); err == nil {
				title = parsedURL.Host
			}
		}
		out = append(out, WebSearchResult{Title: truncateRunes(collapseSpace(title), webSearchMaxTitleRunes), URL: u, Snippet: truncateRunes(collapseSpace(snippet), 300)})
	}
	for _, item := range parsed.Output {
		if item.Type != "message" {
			continue
		}
		for _, c := range item.Content {
			runes := []rune(c.Text)
			for _, ann := range c.Annotations {
				if ann.Type != "url_citation" {
					continue
				}
				add(ann.Title, ann.URL, citedSentence(runes, ann.StartIndex))
			}
		}
	}
	for _, item := range parsed.Output {
		if item.Type != "web_search_call" {
			continue
		}
		for _, src := range item.Action.Sources {
			add(src.Title, src.URL, "")
		}
	}
	return out, nil
}

// citedSentence returns the line of the answer that carries a citation.
func citedSentence(runes []rune, at int) string {
	if at < 0 || at > len(runes) {
		return ""
	}
	start, end := at, at
	for start > 0 && runes[start-1] != '\n' {
		start--
	}
	for end < len(runes) && runes[end] != '\n' {
		end++
	}
	line := strings.TrimSpace(string(runes[start:end]))
	line = strings.TrimLeft(line, "-*0123456789. ")
	// Drop the inline markdown link the model appends to the line.
	if i := strings.Index(line, "(["); i > 0 {
		line = strings.TrimSpace(line[:i])
	}
	return line
}
