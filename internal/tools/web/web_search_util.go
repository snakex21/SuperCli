package web

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

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
