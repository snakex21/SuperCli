package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
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
	hosted  *HostedSearchConfig
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
	webSearchMaxQueries     = 4
)

// NewWebSearch builds the tool. Empty engine = "duckduckgo". baseURL is
// optional and used only by SearXNG; variadic keeps existing callers source
// compatible.
func NewWebSearch(engine, apiKey string, baseURL ...string) *WebSearch {
	engine = strings.ToLower(strings.TrimSpace(engine))
	switch engine {
	case "brave", "tavily", "searxng", "exa":
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
	Queries        []string `json:"queries"`
	MaxResults     int      `json:"max_results"`
	Freshness      string   `json:"freshness"`
	IncludeDomains []string `json:"include_domains"`
	ExcludeDomains []string `json:"exclude_domains"`
	Refresh        bool     `json:"refresh"`
}

type webLookupArgs struct {
	Query   string `json:"query"`
	Refresh bool   `json:"refresh"`
}

// LookupSpec is the tiny always-on path for current facts. It deliberately
// exposes a query and an explicit refresh switch: models can search in one call
// without first spending a turn discovering the larger web_search schema. Advanced filters remain on
// web_search behind tool_search.
func (t *WebSearch) LookupSpec() Tool {
	return Tool{
		Name:        "web_lookup",
		ReadOnly:    true,
		ReuseTTL:    2 * time.Minute,
		RefreshArg:  "refresh",
		NextTools:   []string{"web_fetch", "web_download"},
		Description: "Search the live public web for current facts, new candidate URLs or external references. This does not open, inspect or download a supplied URL: when the user already gave a URL, call web_fetch(url) alone instead of searching. Returns search hits and snippets. Read a known page with web_fetch(url); save a known file URL with web_download(url,path), without another search. Set refresh=true only when a fresh network search is needed instead of reusing a recent result.",
		Schema:      `{"type":"object","properties":{"query":{"type":"string","description":"Search terms. Use a full URL to find external mentions or references to it; use web_fetch to read a known page or web_download to save a known file."},"refresh":{"type":"boolean","description":"Bypass recent result reuse and the search cache to make a fresh network search (default false)."}},"required":["query"]}`,
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
	forward, _ := json.Marshal(webSearchArgs{Query: a.Query, MaxResults: webSearchDefaultResults, Refresh: a.Refresh})
	return t.execute(ctx, forward)
}

// Spec returns the tool registration.
func (t *WebSearch) Spec() Tool {
	return Tool{
		Name:       "web_search",
		ReadOnly:   true,
		ReuseTTL:   2 * time.Minute,
		RefreshArg: "refresh",
		NextTools:  []string{"web_fetch", "web_download"},
		Description: "Search the public web for current facts, new candidate URLs or external references, returning title, URL and snippet. " +
			"This does not open, inspect or download a supplied URL. Read a known page with web_fetch(url); save a known file URL with web_download(url,path), without another search. Default engine needs no API key. " +
			"Several related queries can run in one call (queries). Optional freshness and domain filters narrow results without extra searches. Set refresh=true only when a fresh network search is needed instead of reusing a recent result.",
		Schema: `{
  "type": "object",
  "properties": {
    "query":           {"type": "string", "description": "Search terms. Use a full URL to find external mentions or references to it; use web_fetch to read a known page or web_download to save a known file."},
    "queries":         {"type": "array", "items": {"type": "string"}, "maxItems": 3, "description": "Optional extra related queries (up to 3) searched in parallel with query; results are merged and labelled per query."},
    "max_results":     {"type": "integer", "description": "Number of results (default 5, max 10)."},
    "freshness":       {"type": "string", "enum": ["day", "week", "month", "year"], "description": "Optional recency window."},
    "include_domains": {"type": "array", "items": {"type": "string"}, "maxItems": 10, "description": "Optional domains to include."},
    "exclude_domains": {"type": "array", "items": {"type": "string"}, "maxItems": 10, "description": "Optional domains to exclude."},
    "refresh":         {"type": "boolean", "description": "Bypass recent result reuse and the search cache to make a fresh network search (default false)."}
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
	queries, err := searchQueries(a.Query, a.Queries)
	if err != nil {
		return Result{Err: fmt.Errorf("web_search: %w", err)}, nil
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
	a.IncludeDomains, err = normalizeDomains(a.IncludeDomains)
	if err != nil {
		return Result{Err: fmt.Errorf("web_search: include_domains: %w", err)}, nil
	}
	a.ExcludeDomains, err = normalizeDomains(a.ExcludeDomains)
	if err != nil {
		return Result{Err: fmt.Errorf("web_search: exclude_domains: %w", err)}, nil
	}
	if err := ctx.Err(); err != nil {
		return Result{Err: fmt.Errorf("web_search: %w", err)}, nil
	}
	if len(queries) == 1 {
		a.Query = queries[0]
		value, err := t.runQuery(ctx, a)
		if err != nil {
			return Result{Err: err}, nil
		}
		return Result{Text: formatSearchResults(a.Query, value.engine, value.fallbackFrom, value.results)}, nil
	}
	// Several related queries run in parallel; each keeps its own label and
	// a URL already listed by an earlier query is not repeated.
	values := make([]searchCacheValue, len(queries))
	errs := make([]error, len(queries))
	var wg sync.WaitGroup
	for i, q := range queries {
		wg.Add(1)
		go func(i int, q string) {
			defer wg.Done()
			one := a
			one.Query = q
			values[i], errs[i] = t.runQuery(ctx, one)
		}(i, q)
	}
	wg.Wait()
	return Result{Text: formatMultiSearchResults(queries, values, errs)}, nil
}

// searchQueries merges the required query with up to three extra queries,
// dropping blanks and case-insensitive duplicates.
func searchQueries(query string, extra []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, q := range append([]string{query}, extra...) {
		q = strings.TrimSpace(q)
		key := strings.ToLower(strings.Join(strings.Fields(q), " "))
		if q == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, q)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("query is empty")
	}
	if len(out) > webSearchMaxQueries {
		return nil, fmt.Errorf("at most %d queries per call (query plus %d in queries)", webSearchMaxQueries, webSearchMaxQueries-1)
	}
	return out, nil
}

// runQuery answers one normalized query: cache, then the hosted engine when
// enabled, then the configured engine and the keyless public fallbacks.
func (t *WebSearch) runQuery(ctx context.Context, a webSearchArgs) (searchCacheValue, error) {
	n := a.MaxResults
	cacheKey := t.cacheKey(a)
	cacheGeneration := t.cache.currentGeneration()
	if a.Refresh {
		// A failed fresh request must not silently restore older evidence on
		// the next ordinary call. Invalidate before making the HTTP request.
		cacheGeneration = t.cache.remove(cacheKey)
	} else if cached, ok := t.cache.get(cacheKey, time.Now()); ok {
		return cached, nil
	}

	hostedFrom := ""
	if t.hosted != nil {
		hostedCtx, cancel := context.WithTimeout(ctx, hostedSearchTimeout)
		results, err := t.searchHosted(hostedCtx, a)
		cancel()
		results = filterAndDedupeResults(results, a.IncludeDomains, a.ExcludeDomains, n)
		if err == nil && len(results) > 0 {
			value := searchCacheValue{results: results, engine: t.HostedEngine()}
			t.cache.setIfCurrent(cacheKey, value, searchCacheTTL(a), time.Now(), cacheGeneration)
			return value, nil
		}
		// Any hosted failure (quota, unsupported model, outage, no hits)
		// silently degrades to the local engines below.
		hostedFrom = t.HostedEngine()
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
	fallbackFrom := hostedFrom
	if t.engine == "duckduckgo" && (err != nil || len(results) == 0) {
		duckErr := err
		results, err = t.searchBraveHTML(ctx, a)
		usedEngine = "brave-web"
		if err != nil || len(results) == 0 {
			braveErr := searchAttemptErr(err, results)
			results, err = t.searchBingRSS(ctx, a)
			usedEngine = "bing-rss"
			if err != nil || len(results) == 0 {
				return searchCacheValue{}, fmt.Errorf("web_search public fallbacks failed: duckduckgo: %v; brave-web: %v; bing-rss: %v", searchAttemptErr(duckErr, nil), braveErr, searchAttemptErr(err, results))
			}
		}
		fallbackFrom = joinFallback(hostedFrom, "duckduckgo")
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
					return searchCacheValue{}, fmt.Errorf("web_search (%s failed; public fallbacks failed): primary: %v; duckduckgo: %v; brave-web: %v; bing-rss: %v", t.engine, searchAttemptErr(primaryErr, nil), searchAttemptErr(duckErr, nil), braveErr, searchAttemptErr(err, results))
				}
			}
		}
		usedEngine = fallbackEngine
		fallbackFrom = joinFallback(hostedFrom, t.engine)
	}
	if err != nil {
		return searchCacheValue{}, fmt.Errorf("web_search (%s): %w", t.engine, err)
	}
	results = filterAndDedupeResults(results, a.IncludeDomains, a.ExcludeDomains, n)
	value := searchCacheValue{results: results, engine: usedEngine, fallbackFrom: fallbackFrom}
	t.cache.setIfCurrent(cacheKey, value, searchCacheTTL(a), time.Now(), cacheGeneration)
	return value, nil
}

func joinFallback(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ", ")
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
