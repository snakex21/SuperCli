package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
