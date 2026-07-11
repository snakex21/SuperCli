package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// WebSearch queries a web search engine and returns a ranked list
// of results (title, URL, snippet). It is registered opt-in (NOT
// MarkAlwaysOn); the model discovers it via tool_search.
//
// Engines:
//   - "duckduckgo" (default): scrapes the DuckDuckGo HTML endpoint,
//     no API key required.
//   - "brave":  Brave Search API (X-Subscription-Token header).
//   - "tavily": Tavily Search API (POST JSON with api_key).
//
// The HTTP client reuses the SSRF-guarded dialer from web_fetch so
// a misconfigured engine URL can never reach private addresses.
type WebSearch struct {
	client *http.Client
	engine string
	apiKey string
}

const (
	webSearchTimeout        = 20 * time.Second
	webSearchDefaultResults = 5
	webSearchMaxResults     = 10
	webSearchMaxBody        = 2 << 20 // 2 MB
)

// NewWebSearch builds the tool. Empty engine = "duckduckgo".
func NewWebSearch(engine, apiKey string) *WebSearch {
	engine = strings.ToLower(strings.TrimSpace(engine))
	switch engine {
	case "brave", "tavily":
		// keyed engines; key checked at call time
	default:
		engine = "duckduckgo"
	}
	fetch := NewWebFetch() // reuse its SSRF-guarded transport
	return &WebSearch{
		client: fetch.client,
		engine: engine,
		apiKey: strings.TrimSpace(apiKey),
	}
}

// WebSearchResult is one search hit.
type WebSearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

type webSearchArgs struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
}

// Spec returns the tool registration.
func (t *WebSearch) Spec() Tool {
	return Tool{
		Name: "web_search",
		Description: "Search the web and return a list of results (title, URL, snippet). " +
			"Use for current events, documentation lookups, or anything not in your training data. " +
			"Follow up with web_fetch to read a result in full. Default engine needs no API key.",
		Schema: `{
  "type": "object",
  "properties": {
    "query":       {"type": "string", "description": "Search query, e.g. 'golang bubbletea textarea height'."},
    "max_results": {"type": "integer", "description": "Number of results to return (default 5, max 10)."}
  },
  "required": ["query"]
}`,
		Fn: t.execute,
	}
}

func (t *WebSearch) execute(ctx context.Context, args json.RawMessage) (Result, error) {
	var a webSearchArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("web_search: bad args: %w", err)}, nil
	}
	q := strings.TrimSpace(a.Query)
	if q == "" {
		return Result{Err: fmt.Errorf("web_search: query is empty")}, nil
	}
	n := a.MaxResults
	if n <= 0 {
		n = webSearchDefaultResults
	}
	if n > webSearchMaxResults {
		n = webSearchMaxResults
	}

	ctx, cancel := context.WithTimeout(ctx, webSearchTimeout)
	defer cancel()

	var (
		results []WebSearchResult
		err     error
	)
	switch t.engine {
	case "brave":
		results, err = t.searchBrave(ctx, q, n)
	case "tavily":
		results, err = t.searchTavily(ctx, q, n)
	default:
		results, err = t.searchDuckDuckGo(ctx, q, n)
	}
	if err != nil {
		return Result{Err: fmt.Errorf("web_search (%s): %w", t.engine, err)}, nil
	}
	if len(results) == 0 {
		return Result{Text: fmt.Sprintf("No results for %q (engine: %s).", q, t.engine)}, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Results for %q (engine: %s):\n\n", q, t.engine)
	for i, r := range results {
		fmt.Fprintf(&sb, "%d. %s\n   %s\n", i+1, r.Title, r.URL)
		if r.Snippet != "" {
			fmt.Fprintf(&sb, "   %s\n", r.Snippet)
		}
	}
	sb.WriteString("\nUse web_fetch with a result URL to read the full page.")
	return Result{Text: sb.String()}, nil
}

// --- DuckDuckGo (HTML scrape, no key) --------------------------------------

var (
	ddgResultRe  = regexp.MustCompile(`(?s)<a[^>]*class="result__a"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	ddgSnippetRe = regexp.MustCompile(`(?s)<a[^>]*class="result__snippet"[^>]*>(.*?)</a>|<td[^>]*class="result-snippet"[^>]*>(.*?)</td>`)
	tagRe        = regexp.MustCompile(`<[^>]+>`)
)

func (t *WebSearch) searchDuckDuckGo(ctx context.Context, query string, n int) ([]WebSearchResult, error) {
	u := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) SuperCli/0.6 web_search")
	req.Header.Set("Accept", "text/html")
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, requestFailedErr(err, "html.duckduckgo.com")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, httpFailedErr(resp, "html.duckduckgo.com")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, webSearchMaxBody))
	if err != nil {
		return nil, err
	}
	return parseDuckDuckGoHTML(string(body), n), nil
}

// parseDuckDuckGoHTML extracts results from the DDG html endpoint.
// Pure; unit-testable.
func parseDuckDuckGoHTML(body string, n int) []WebSearchResult {
	links := ddgResultRe.FindAllStringSubmatch(body, -1)
	snips := ddgSnippetRe.FindAllStringSubmatch(body, -1)
	out := make([]WebSearchResult, 0, n)
	for i, m := range links {
		if len(out) >= n {
			break
		}
		href := decodeDDGHref(m[1])
		title := cleanHTMLFragment(m[2])
		if href == "" || title == "" {
			continue
		}
		snippet := ""
		if i < len(snips) {
			s := snips[i][1]
			if s == "" {
				s = snips[i][2]
			}
			snippet = cleanHTMLFragment(s)
		}
		out = append(out, WebSearchResult{Title: title, URL: href, Snippet: snippet})
	}
	return out
}

// decodeDDGHref unwraps DuckDuckGo redirect links of the form
// //duckduckgo.com/l/?uddg=<urlencoded>&rut=... into the real URL.
func decodeDDGHref(href string) string {
	href = html.UnescapeString(href)
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	u, err := url.Parse(href)
	if err != nil {
		return href
	}
	if strings.Contains(u.Host, "duckduckgo.com") {
		if real := u.Query().Get("uddg"); real != "" {
			return real
		}
	}
	return href
}

func cleanHTMLFragment(s string) string {
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

// --- Brave Search API -------------------------------------------------------

func (t *WebSearch) searchBrave(ctx context.Context, query string, n int) ([]WebSearchResult, error) {
	if t.apiKey == "" {
		return nil, fmt.Errorf("brave engine requires an API key ([web_search] api_key in config.toml or BRAVE_API_KEY)")
	}
	u := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=%d", url.QueryEscape(query), n)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", t.apiKey)
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, requestFailedErr(err, "api.search.brave.com")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, httpFailedErr(resp, "api.search.brave.com")
	}
	var parsed struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, webSearchMaxBody)).Decode(&parsed); err != nil {
		return nil, err
	}
	out := make([]WebSearchResult, 0, n)
	for _, r := range parsed.Web.Results {
		if len(out) >= n {
			break
		}
		out = append(out, WebSearchResult{
			Title:   cleanHTMLFragment(r.Title),
			URL:     r.URL,
			Snippet: cleanHTMLFragment(r.Description),
		})
	}
	return out, nil
}

// --- Tavily Search API ------------------------------------------------------

func (t *WebSearch) searchTavily(ctx context.Context, query string, n int) ([]WebSearchResult, error) {
	if t.apiKey == "" {
		return nil, fmt.Errorf("tavily engine requires an API key ([web_search] api_key in config.toml or TAVILY_API_KEY)")
	}
	payload, err := json.Marshal(map[string]any{
		"api_key":     t.apiKey,
		"query":       query,
		"max_results": n,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.tavily.com/search", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, requestFailedErr(err, "api.tavily.com")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, httpFailedErr(resp, "api.tavily.com")
	}
	var parsed struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, webSearchMaxBody)).Decode(&parsed); err != nil {
		return nil, err
	}
	out := make([]WebSearchResult, 0, n)
	for _, r := range parsed.Results {
		if len(out) >= n {
			break
		}
		snippet := r.Content
		if len(snippet) > 300 {
			snippet = snippet[:300] + "…"
		}
		out = append(out, WebSearchResult{Title: r.Title, URL: r.URL, Snippet: snippet})
	}
	return out, nil
}
