package web

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNormalizeDomainsAndQuery(t *testing.T) {
	domains, err := normalizeDomains([]string{"https://Docs.Example.com/path", "*.example.org", "docs.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(domains, ","), "docs.example.com,example.org"; got != want {
		t.Fatalf("domains = %q, want %q", got, want)
	}
	query := queryWithDomainFilters("go cache", domains, []string{"spam.example"})
	if !strings.Contains(query, "(site:docs.example.com OR site:example.org)") || !strings.Contains(query, "-site:spam.example") {
		t.Fatalf("domain query not constructed: %q", query)
	}
}

func TestFilterAndDedupeResults(t *testing.T) {
	results := []WebSearchResult{
		{Title: "A", URL: "https://www.example.com/doc?utm_source=x", Snippet: "one"},
		{Title: "A duplicate", URL: "https://example.com/doc", Snippet: "two"},
		{Title: "Blocked", URL: "https://ads.example.com/x"},
		{Title: "Other", URL: "https://other.example.net/x"},
	}
	got := filterAndDedupeResults(results, []string{"example.com"}, []string{"ads.example.com"}, 10)
	if len(got) != 1 || got[0].Title != "A" {
		t.Fatalf("filtered results = %#v", got)
	}
}

func TestSearchCacheExpiryAndLRU(t *testing.T) {
	now := time.Unix(100, 0)
	cache := newSearchCache(2)
	cache.set("a", searchCacheValue{engine: "a"}, time.Minute, now)
	cache.set("b", searchCacheValue{engine: "b"}, time.Minute, now)
	if _, ok := cache.get("a", now); !ok { // a becomes newest
		t.Fatal("cache miss before expiry")
	}
	cache.set("c", searchCacheValue{engine: "c"}, time.Minute, now)
	if _, ok := cache.get("b", now); ok {
		t.Fatal("least recently used entry was not evicted")
	}
	if _, ok := cache.get("a", now.Add(time.Minute)); ok {
		t.Fatal("expired entry returned")
	}
}

func TestShouldFallbackSearch(t *testing.T) {
	for _, msg := range []string{
		"request_failed cause=timeout host=x",
		"http_failed status=429 host=x",
		"http_failed status=503 host=x",
		"invalid character in response",
	} {
		if !shouldFallbackSearch(assertErr(msg)) {
			t.Errorf("should fallback for %q", msg)
		}
	}
	for _, msg := range []string{
		"brave engine requires an API key",
		"http_failed status=401 host=x",
		"searxng engine requires [web_search] base_url",
	} {
		if shouldFallbackSearch(assertErr(msg)) {
			t.Errorf("must expose permanent error %q", msg)
		}
	}
}

type assertErr string

func (e assertErr) Error() string { return string(e) }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestSearXNGRequest(t *testing.T) {
	tool := NewWebSearch("searxng", "secret", "https://search.example.com/root")
	tool.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/root/search" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		if req.URL.Query().Get("format") != "json" || req.URL.Query().Get("time_range") != "week" {
			t.Fatalf("query = %q", req.URL.RawQuery)
		}
		if !strings.Contains(req.URL.Query().Get("q"), "site:go.dev") {
			t.Fatalf("domain filter missing: %q", req.URL.Query().Get("q"))
		}
		if req.Header.Get("Authorization") != "Bearer secret" {
			t.Fatal("optional authorization header missing")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"results":[{"title":"Go docs","url":"https://go.dev/doc","content":"Official docs"}]}`)),
			Request:    req,
		}, nil
	})}
	got, err := tool.searchSearXNG(context.Background(), webSearchArgs{
		Query: "golang", MaxResults: 5, Freshness: "week", IncludeDomains: []string{"go.dev"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].URL != "https://go.dev/doc" {
		t.Fatalf("results = %#v", got)
	}
}

func TestWebLookupUsesCompactContractAndSharedSearch(t *testing.T) {
	tool := NewWebSearch("searxng", "", "https://search.example.com")
	tool.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.URL.Query().Get("q"); got != "second World Cup semifinal 2026" {
			t.Fatalf("query = %q", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				`{"results":[{"title":"Official schedule","url":"https://example.com/schedule","content":"15 July 2026"}]}`)),
			Request: req,
		}, nil
	})}

	spec := tool.LookupSpec()
	if spec.Name != "web_lookup" || !spec.ReadOnly || strings.Contains(spec.Schema, "freshness") {
		t.Fatalf("unexpected compact spec: %+v", spec)
	}
	result, err := spec.Fn(context.Background(), []byte(`{"query":"second World Cup semifinal 2026"}`))
	if err != nil || result.Err != nil || !strings.Contains(result.Text, "15 July 2026") {
		t.Fatalf("lookup = %+v, err=%v", result, err)
	}
}

func TestSearchCacheTTL(t *testing.T) {
	if got := searchCacheTTL(webSearchArgs{Query: "go interface tutorial"}); got != webSearchReferenceTTL {
		t.Fatalf("reference TTL = %s", got)
	}
	if got := searchCacheTTL(webSearchArgs{Query: "latest go release"}); got != webSearchFreshTTL {
		t.Fatalf("fresh TTL = %s", got)
	}
}
