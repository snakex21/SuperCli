package web

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
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
//   - "searxng": a user-provided public SearXNG instance.
//
// The HTTP client reuses the SSRF-guarded dialer from web_fetch so
// a misconfigured engine URL can never reach private addresses.
type WebSearch struct {
	client  *http.Client
	engine  string
	apiKey  string
	baseURL string
	cache   *searchCache
}

const (
	webSearchTimeout        = 20 * time.Second
	webSearchPrimaryTimeout = 12 * time.Second
	webSearchDefaultResults = 5
	webSearchMaxResults     = 10
	webSearchMaxBody        = 2 << 20 // 2 MB
	webSearchFreshTTL       = 30 * time.Minute
	webSearchReferenceTTL   = 24 * time.Hour
	webSearchMaxDomains     = 10
	webSearchMaxResultURL   = 4096
	webSearchMaxTitleRunes  = 200
)

// NewWebSearch builds the tool. Empty engine = "duckduckgo". baseURL is
// optional and used only by SearXNG; variadic keeps existing callers source
// compatible.
func NewWebSearch(engine, apiKey string, baseURL ...string) *WebSearch {
	engine = strings.ToLower(strings.TrimSpace(engine))
	switch engine {
	case "brave", "tavily", "searxng":
		// keyed engines; key checked at call time
	default:
		engine = "duckduckgo"
	}
	var endpoint string
	if len(baseURL) > 0 {
		endpoint = strings.TrimSpace(baseURL[0])
	}
	fetch := NewWebFetch() // reuse its SSRF-guarded transport
	return &WebSearch{
		client:  fetch.client,
		engine:  engine,
		apiKey:  strings.TrimSpace(apiKey),
		baseURL: endpoint,
		cache:   newSearchCache(webSearchCacheEntries),
	}
}

// WebSearchResult is one search hit.
type WebSearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

type webSearchArgs struct {
	Query          string   `json:"query"`
	MaxResults     int      `json:"max_results"`
	Freshness      string   `json:"freshness"`
	IncludeDomains []string `json:"include_domains"`
	ExcludeDomains []string `json:"exclude_domains"`
}

type webLookupArgs struct {
	Query string `json:"query"`
}

// LookupSpec is the tiny always-on path for current facts. It deliberately
// exposes only a query: models can search in one call without first spending a
// turn discovering the larger web_search schema. Advanced filters remain on
// web_search behind tool_search.
func (t *WebSearch) LookupSpec() Tool {
	return Tool{
		Name:        "web_lookup",
		ReadOnly:    true,
		Description: "Search the live public web for current facts. Use before answering time-sensitive questions.",
		Schema:      `{"type":"object","properties":{"query":{"type":"string","description":"What to look up on the web."}},"required":["query"]}`,
		Fn:          t.lookup,
	}
}

func (t *WebSearch) lookup(ctx context.Context, raw json.RawMessage) (Result, error) {
	var a webLookupArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return Result{Err: fmt.Errorf("web_lookup: bad args: %w", err)}, nil
	}
	a.Query = strings.TrimSpace(a.Query)
	if a.Query == "" {
		return Result{Err: fmt.Errorf("web_lookup: query is empty")}, nil
	}
	forward, _ := json.Marshal(webSearchArgs{Query: a.Query, MaxResults: webSearchDefaultResults})
	return t.execute(ctx, forward)
}

// Spec returns the tool registration.
func (t *WebSearch) Spec() Tool {
	return Tool{
		Name:     "web_search",
		ReadOnly: true,
		Description: "Search the web and return a list of results (title, URL, snippet). " +
			"Use for current events, documentation lookups, or anything not in your training data. " +
			"Follow up with web_fetch to read a result in full. Default engine needs no API key. " +
			"Optional freshness and domain filters narrow results without extra searches.",
		Schema: `{
  "type": "object",
  "properties": {
    "query":           {"type": "string", "description": "Search query, e.g. 'golang bubbletea textarea height'."},
    "max_results":     {"type": "integer", "description": "Number of results (default 5, max 10)."},
    "freshness":       {"type": "string", "enum": ["day", "week", "month", "year"], "description": "Optional recency window."},
    "include_domains": {"type": "array", "items": {"type": "string"}, "maxItems": 10, "description": "Optional domains to include."},
    "exclude_domains": {"type": "array", "items": {"type": "string"}, "maxItems": 10, "description": "Optional domains to exclude."}
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
	a.MaxResults = n
	a.Freshness = strings.ToLower(strings.TrimSpace(a.Freshness))
	if a.Freshness != "" && a.Freshness != "day" && a.Freshness != "week" && a.Freshness != "month" && a.Freshness != "year" {
		return Result{Err: fmt.Errorf("web_search: freshness must be day, week, month, or year")}, nil
	}
	var err error
	a.IncludeDomains, err = normalizeDomains(a.IncludeDomains)
	if err != nil {
		return Result{Err: fmt.Errorf("web_search: include_domains: %w", err)}, nil
	}
	a.ExcludeDomains, err = normalizeDomains(a.ExcludeDomains)
	if err != nil {
		return Result{Err: fmt.Errorf("web_search: exclude_domains: %w", err)}, nil
	}
	cacheKey := t.cacheKey(a)
	if cached, ok := t.cache.get(cacheKey, time.Now()); ok {
		return Result{Text: formatSearchResults(q, cached.engine, cached.fallbackFrom, cached.results)}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, webSearchTimeout)
	defer cancel()

	searchCtx := ctx
	cancelPrimary := func() {}
	if t.engine != "duckduckgo" {
		searchCtx, cancelPrimary = context.WithTimeout(ctx, webSearchPrimaryTimeout)
	}
	results, err := t.search(searchCtx, a)
	cancelPrimary()
	results = filterAndDedupeResults(results, a.IncludeDomains, a.ExcludeDomains, n)
	usedEngine := t.engine
	fallbackFrom := ""
	if t.engine == "duckduckgo" && (err != nil || len(results) == 0) {
		duckErr := err
		results, err = t.searchBraveHTML(ctx, a)
		usedEngine = "brave-web"
		if err != nil || len(results) == 0 {
			braveErr := searchAttemptErr(err, results)
			results, err = t.searchBingRSS(ctx, a)
			usedEngine = "bing-rss"
			if err != nil || len(results) == 0 {
				return Result{Err: fmt.Errorf("web_search public fallbacks failed: duckduckgo: %v; brave-web: %v; bing-rss: %v", searchAttemptErr(duckErr, nil), braveErr, searchAttemptErr(err, results))}, nil
			}
		}
		fallbackFrom = "duckduckgo"
	}
	if t.engine != "duckduckgo" && ((err != nil && shouldFallbackSearch(err)) || (err == nil && len(results) == 0)) {
		primaryErr := err
		results, err = t.searchDuckDuckGo(ctx, a)
		fallbackEngine := "duckduckgo"
		if err != nil || len(results) == 0 {
			duckErr := err
			results, err = t.searchBraveHTML(ctx, a)
			fallbackEngine = "brave-web"
			if err != nil || len(results) == 0 {
				braveErr := searchAttemptErr(err, results)
				results, err = t.searchBingRSS(ctx, a)
				fallbackEngine = "bing-rss"
				if err != nil || len(results) == 0 {
					return Result{Err: fmt.Errorf("web_search (%s failed; public fallbacks failed): primary: %v; duckduckgo: %v; brave-web: %v; bing-rss: %v", t.engine, searchAttemptErr(primaryErr, nil), searchAttemptErr(duckErr, nil), braveErr, searchAttemptErr(err, results))}, nil
				}
			}
		}
		usedEngine = fallbackEngine
		fallbackFrom = t.engine
	}
	if err != nil {
		return Result{Err: fmt.Errorf("web_search (%s): %w", t.engine, err)}, nil
	}
	results = filterAndDedupeResults(results, a.IncludeDomains, a.ExcludeDomains, n)
	value := searchCacheValue{results: results, engine: usedEngine, fallbackFrom: fallbackFrom}
	t.cache.set(cacheKey, value, searchCacheTTL(a), time.Now())
	return Result{Text: formatSearchResults(q, usedEngine, fallbackFrom, results)}, nil
}

func searchAttemptErr(err error, results []WebSearchResult) error {
	if err != nil {
		return err
	}
	if len(results) == 0 {
		return fmt.Errorf("returned no results")
	}
	return nil
}

func (t *WebSearch) search(ctx context.Context, a webSearchArgs) ([]WebSearchResult, error) {
	switch t.engine {
	case "brave":
		return t.searchBrave(ctx, a)
	case "tavily":
		return t.searchTavily(ctx, a)
	case "searxng":
		return t.searchSearXNG(ctx, a)
	default:
		return t.searchDuckDuckGo(ctx, a)
	}
}

func formatSearchResults(query, engine, fallbackFrom string, results []WebSearchResult) string {
	if len(results) == 0 {
		return fmt.Sprintf("No results for %q (engine: %s).", query, engine)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Results for %q (engine: %s", query, engine)
	if fallbackFrom != "" {
		fmt.Fprintf(&sb, ", fallback from %s", fallbackFrom)
	}
	sb.WriteString("):\n\n")
	for i, r := range results {
		fmt.Fprintf(&sb, "%d. %s\n   %s\n", i+1, r.Title, r.URL)
		if r.Snippet != "" {
			fmt.Fprintf(&sb, "   %s\n", r.Snippet)
		}
	}
	sb.WriteString("\nUse web_fetch with a result URL to read the full page.")
	return sb.String()
}

// --- DuckDuckGo (HTML scrape, no key) --------------------------------------

var (
	ddgResultRe    = regexp.MustCompile(`(?s)<a[^>]*class="result__a"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	ddgSnippetRe   = regexp.MustCompile(`(?s)<a[^>]*class="result__snippet"[^>]*>(.*?)</a>|<td[^>]*class="result-snippet"[^>]*>(.*?)</td>`)
	braveStartRe   = regexp.MustCompile(`(?s)<div class="snippet[^"]*"[^>]*data-type="web"[^>]*>`)
	braveTitleRe   = regexp.MustCompile(`(?s)<a[^>]*href="([^"]+)"[^>]*>.*?<div class="title search-snippet-title[^"]*"[^>]*>(.*?)</div>`)
	braveSnippetRe = regexp.MustCompile(`(?s)<div class="generic-snippet[^"]*"[^>]*>.*?<div class="content[^"]*"[^>]*>(.*?)</div>`)
	tagRe          = regexp.MustCompile(`<[^>]+>`)
)

func (t *WebSearch) searchDuckDuckGo(ctx context.Context, a webSearchArgs) ([]WebSearchResult, error) {
	u, _ := url.Parse("https://html.duckduckgo.com/html/")
	params := u.Query()
	params.Set("q", queryWithDomainFilters(a.Query, a.IncludeDomains, a.ExcludeDomains))
	if df := duckDuckGoFreshness(a.Freshness); df != "" {
		params.Set("df", df)
	}
	u.RawQuery = params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
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
	return parseDuckDuckGoHTML(string(body), a.MaxResults), nil
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

// searchBraveHTML is a no-key fallback for environments where DuckDuckGo's
// HTML endpoint rate-limits or challenges non-browser clients. It uses Brave's
// public web results page, not the keyed Brave Search API.
func (t *WebSearch) searchBraveHTML(ctx context.Context, a webSearchArgs) ([]WebSearchResult, error) {
	u, _ := url.Parse("https://search.brave.com/search")
	params := u.Query()
	params.Set("q", queryWithDomainFilters(a.Query, a.IncludeDomains, a.ExcludeDomains))
	params.Set("source", "web")
	u.RawQuery = params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126 Safari/537.36")
	req.Header.Set("Accept", "text/html")
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, requestFailedErr(err, "search.brave.com")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, httpFailedErr(resp, "search.brave.com")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, webSearchMaxBody))
	if err != nil {
		return nil, err
	}
	return parseBraveHTML(string(body), a.MaxResults), nil
}

func parseBraveHTML(body string, n int) []WebSearchResult {
	starts := braveStartRe.FindAllStringIndex(body, -1)
	out := make([]WebSearchResult, 0, min(n, len(starts)))
	for i, start := range starts {
		if len(out) >= n {
			break
		}
		end := len(body)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		block := body[start[0]:end]
		match := braveTitleRe.FindStringSubmatch(block)
		if len(match) < 3 {
			continue
		}
		href := html.UnescapeString(match[1])
		title := cleanHTMLFragment(match[2])
		if href == "" || title == "" {
			continue
		}
		snippet := ""
		if match := braveSnippetRe.FindStringSubmatch(block); len(match) > 1 {
			snippet = cleanHTMLFragment(match[1])
		}
		out = append(out, WebSearchResult{Title: title, URL: href, Snippet: snippet})
	}
	return out
}

// searchBingRSS is the last no-key fallback. Bing's RSS representation avoids
// the JavaScript/challenge markup used by its normal result page and is much
// less brittle to parse than another HTML scraper.
func (t *WebSearch) searchBingRSS(ctx context.Context, a webSearchArgs) ([]WebSearchResult, error) {
	u, _ := url.Parse("https://www.bing.com/search")
	params := u.Query()
	params.Set("q", queryWithDomainFilters(a.Query, a.IncludeDomains, a.ExcludeDomains))
	params.Set("format", "rss")
	u.RawQuery = params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) SuperCli/0.6 web_search")
	req.Header.Set("Accept", "application/rss+xml, application/xml;q=0.9")
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, requestFailedErr(err, "www.bing.com")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, httpFailedErr(resp, "www.bing.com")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, webSearchMaxBody))
	if err != nil {
		return nil, err
	}
	return parseBingRSS(body, a.MaxResults)
}

func parseBingRSS(body []byte, n int) ([]WebSearchResult, error) {
	var feed struct {
		Channel struct {
			Items []struct {
				Title       string `xml:"title"`
				Link        string `xml:"link"`
				Description string `xml:"description"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("parse bing rss: %w", err)
	}
	out := make([]WebSearchResult, 0, min(n, len(feed.Channel.Items)))
	for _, item := range feed.Channel.Items {
		if len(out) >= n {
			break
		}
		title := cleanHTMLFragment(item.Title)
		href := strings.TrimSpace(item.Link)
		if title == "" || href == "" {
			continue
		}
		out = append(out, WebSearchResult{Title: title, URL: href, Snippet: cleanHTMLFragment(item.Description)})
	}
	return out, nil
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

func (t *WebSearch) searchBrave(ctx context.Context, a webSearchArgs) ([]WebSearchResult, error) {
	if t.apiKey == "" {
		return nil, fmt.Errorf("brave engine requires an API key ([web_search] api_key in config.toml or BRAVE_API_KEY)")
	}
	u, _ := url.Parse("https://api.search.brave.com/res/v1/web/search")
	params := u.Query()
	params.Set("q", queryWithDomainFilters(a.Query, a.IncludeDomains, a.ExcludeDomains))
	params.Set("count", strconv.Itoa(a.MaxResults))
	if fresh := braveFreshness(a.Freshness); fresh != "" {
		params.Set("freshness", fresh)
	}
	u.RawQuery = params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
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
	out := make([]WebSearchResult, 0, a.MaxResults)
	for _, r := range parsed.Web.Results {
		if len(out) >= a.MaxResults {
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

func (t *WebSearch) searchTavily(ctx context.Context, a webSearchArgs) ([]WebSearchResult, error) {
	if t.apiKey == "" {
		return nil, fmt.Errorf("tavily engine requires an API key ([web_search] api_key in config.toml or TAVILY_API_KEY)")
	}
	request := map[string]any{
		"api_key":     t.apiKey,
		"query":       a.Query,
		"max_results": a.MaxResults,
	}
	if a.Freshness != "" {
		request["time_range"] = a.Freshness
	}
	if len(a.IncludeDomains) > 0 {
		request["include_domains"] = a.IncludeDomains
	}
	if len(a.ExcludeDomains) > 0 {
		request["exclude_domains"] = a.ExcludeDomains
	}
	payload, err := json.Marshal(request)
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
	out := make([]WebSearchResult, 0, a.MaxResults)
	for _, r := range parsed.Results {
		if len(out) >= a.MaxResults {
			break
		}
		snippet := r.Content
		if len(snippet) > 300 {
			snippet = truncateRunes(snippet, 300)
		}
		out = append(out, WebSearchResult{Title: cleanHTMLFragment(r.Title), URL: r.URL, Snippet: cleanHTMLFragment(snippet)})
	}
	return out, nil
}

// --- SearXNG ---------------------------------------------------------------

func (t *WebSearch) searchSearXNG(ctx context.Context, a webSearchArgs) ([]WebSearchResult, error) {
	if t.baseURL == "" {
		return nil, fmt.Errorf("searxng engine requires [web_search] base_url")
	}
	u, err := validateFetchURL(t.baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid searxng base_url: %w", err)
	}
	if !strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/search") {
		u.Path = strings.TrimRight(u.Path, "/") + "/search"
	}
	params := u.Query()
	params.Set("q", queryWithDomainFilters(a.Query, a.IncludeDomains, a.ExcludeDomains))
	params.Set("format", "json")
	params.Set("language", "all")
	if a.Freshness != "" {
		params.Set("time_range", a.Freshness)
	}
	u.RawQuery = params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if t.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+t.apiKey)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, requestFailedErr(err, u.Host)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, httpFailedErr(resp, u.Host)
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
	out := make([]WebSearchResult, 0, a.MaxResults)
	for _, r := range parsed.Results {
		if len(out) >= a.MaxResults {
			break
		}
		out = append(out, WebSearchResult{
			Title:   cleanHTMLFragment(r.Title),
			URL:     r.URL,
			Snippet: cleanHTMLFragment(truncateRunes(r.Content, 300)),
		})
	}
	return out, nil
}

func (t *WebSearch) cacheKey(a webSearchArgs) string {
	return strings.Join([]string{
		t.engine,
		strings.ToLower(strings.TrimSpace(t.baseURL)),
		strings.ToLower(strings.Join(strings.Fields(a.Query), " ")),
		strconv.Itoa(a.MaxResults),
		a.Freshness,
		strings.Join(a.IncludeDomains, ","),
		strings.Join(a.ExcludeDomains, ","),
	}, "\x00")
}

func searchCacheTTL(a webSearchArgs) time.Duration {
	if a.Freshness != "" {
		return webSearchFreshTTL
	}
	q := strings.ToLower(a.Query)
	for _, marker := range []string{
		" latest", "current ", " current", "today", "news", "release",
		"version", "security", "advisory", "cve-", "update", "price",
	} {
		if strings.Contains(" "+q+" ", marker) {
			return webSearchFreshTTL
		}
	}
	return webSearchReferenceTTL
}

func normalizeDomains(domains []string) ([]string, error) {
	if len(domains) > webSearchMaxDomains {
		return nil, fmt.Errorf("at most %d domains are allowed", webSearchMaxDomains)
	}
	seen := make(map[string]struct{}, len(domains))
	out := make([]string, 0, len(domains))
	for _, raw := range domains {
		raw = strings.TrimSpace(strings.ToLower(raw))
		if raw == "" {
			continue
		}
		candidate := raw
		if !strings.Contains(candidate, "://") {
			candidate = "https://" + candidate
		}
		u, err := url.Parse(candidate)
		if err != nil || u.Hostname() == "" {
			return nil, fmt.Errorf("invalid domain %q", raw)
		}
		domain := strings.TrimPrefix(strings.TrimSuffix(u.Hostname(), "."), "*.")
		if domain == "" || strings.ContainsAny(domain, " /\\@") {
			return nil, fmt.Errorf("invalid domain %q", raw)
		}
		for _, r := range domain {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '.' && r != '-' && r != ':' {
				return nil, fmt.Errorf("invalid domain %q", raw)
			}
		}
		if _, ok := seen[domain]; ok {
			continue
		}
		seen[domain] = struct{}{}
		out = append(out, domain)
	}
	sort.Strings(out)
	return out, nil
}

func queryWithDomainFilters(query string, include, exclude []string) string {
	var parts []string
	parts = append(parts, strings.TrimSpace(query))
	if len(include) == 1 {
		parts = append(parts, "site:"+include[0])
	} else if len(include) > 1 {
		sites := make([]string, 0, len(include))
		for _, domain := range include {
			sites = append(sites, "site:"+domain)
		}
		parts = append(parts, "("+strings.Join(sites, " OR ")+")")
	}
	for _, domain := range exclude {
		parts = append(parts, "-site:"+domain)
	}
	return strings.Join(parts, " ")
}

func duckDuckGoFreshness(freshness string) string {
	return map[string]string{"day": "d", "week": "w", "month": "m", "year": "y"}[freshness]
}

func braveFreshness(freshness string) string {
	return map[string]string{"day": "pd", "week": "pw", "month": "pm", "year": "py"}[freshness]
}

func filterAndDedupeResults(results []WebSearchResult, include, exclude []string, limit int) []WebSearchResult {
	seen := make(map[string]struct{}, len(results))
	out := make([]WebSearchResult, 0, min(limit, len(results)))
	for _, result := range results {
		result.URL = strings.TrimSpace(result.URL)
		if result.URL == "" || len(result.URL) > webSearchMaxResultURL {
			continue
		}
		host, key := resultHostAndKey(result.URL)
		if len(include) > 0 && !matchesAnyDomain(host, include) {
			continue
		}
		if matchesAnyDomain(host, exclude) {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result.Title = truncateRunes(cleanHTMLFragment(result.Title), webSearchMaxTitleRunes)
		result.Snippet = cleanHTMLFragment(truncateRunes(result.Snippet, 300))
		out = append(out, result)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func resultHostAndKey(raw string) (string, string) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "", strings.ToLower(strings.TrimSpace(raw))
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	keyHost := strings.TrimPrefix(host, "www.")
	if port := u.Port(); port != "" {
		keyHost = net.JoinHostPort(keyHost, port)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = keyHost
	u.Fragment = ""
	query := u.Query()
	for key := range query {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "utm_") || lower == "fbclid" || lower == "gclid" || lower == "mc_cid" || lower == "mc_eid" {
			query.Del(key)
		}
	}
	u.RawQuery = query.Encode()
	return host, u.String()
}

func matchesAnyDomain(host string, domains []string) bool {
	for _, domain := range domains {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func shouldFallbackSearch(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, permanent := range []string{
		"requires an api key", "requires [web_search] base_url", "invalid searxng base_url",
		"private or local", "cause=canceled", "status=400", "status=401", "status=403", "status=404",
	} {
		if strings.Contains(msg, permanent) {
			return false
		}
	}
	return true
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}
