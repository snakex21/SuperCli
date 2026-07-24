package web

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

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
