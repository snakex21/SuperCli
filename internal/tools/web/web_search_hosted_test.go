package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

const hostedFixture = `{"output":[
 {"type":"web_search_call","action":{"type":"search","sources":[{"type":"url","url":"https://go.dev/dl/"},{"type":"url","url":"https://go.dev/doc/devel/release"}]}},
 {"type":"message","content":[{"type":"output_text","text":"- Go 1.27.4 was released on 2026-11-10 ([go.dev](https://go.dev/doc/devel/release?utm_source=openai))\n- Downloads are listed on the download page.","annotations":[
   {"type":"url_citation","url":"https://go.dev/doc/devel/release?utm_source=openai","title":"Release History","start_index":2,"end_index":40},
   {"type":"url_citation","url":"https://go.dev/dl/?utm_source=openai","title":"All releases","start_index":95,"end_index":110}]}]}]}`

func searchJSON(t *testing.T, fn func(context.Context, json.RawMessage) (Result, error), args map[string]any) Result {
	t.Helper()
	raw, _ := json.Marshal(args)
	result, err := fn(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestHostedSearchAnswersThroughTheSameTool(t *testing.T) {
	var hosted, local int32
	tool := NewWebSearch("searxng", "", "https://search.example.org").WithHosted(HostedSearchConfig{Provider: "openai", APIKey: "test-key"})
	tool.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "api.openai.com" {
			atomic.AddInt32(&hosted, 1)
			body, _ := io.ReadAll(req.Body)
			var sent map[string]any
			_ = json.Unmarshal(body, &sent)
			if req.URL.Path != "/v1/responses" || req.Header.Get("Authorization") != "Bearer test-key" || sent["model"] != "gpt-5.4-nano" || !strings.Contains(string(body), `"web_search"`) {
				t.Errorf("unexpected hosted request %s %s", req.URL, body)
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(hostedFixture)), Request: req}, nil
		}
		atomic.AddInt32(&local, 1)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"results":[]}`)), Request: req}, nil
	})}
	for _, fn := range []func(context.Context, json.RawMessage) (Result, error){tool.Spec().Fn, tool.LookupSpec().Fn} {
		result := searchJSON(t, fn, map[string]any{"query": "latest Go release", "refresh": true})
		text := result.Text
		if result.Err != nil || !strings.Contains(text, "(engine: openai-hosted)") ||
			!strings.Contains(text, "1. Release History\n   https://go.dev/doc/devel/release\n   Go 1.27.4 was released on 2026-11-10") ||
			!strings.Contains(text, "https://go.dev/dl/") || strings.Contains(text, "utm_source") {
			t.Fatalf("hosted result not rendered as ordinary search hits:\n%s %v", text, result.Err)
		}
	}
	if hosted != 2 || local != 0 {
		t.Fatalf("hosted=%d local=%d", hosted, local)
	}
}

func TestHostedSearchFallsBackToLocalEngines(t *testing.T) {
	for name, hostedReply := range map[string]func(*http.Request) (*http.Response, error){
		"quota": func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 429, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"quota"}}`)), Request: req}, nil
		},
		"empty": func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"output":[]}`)), Request: req}, nil
		},
		"network": func(req *http.Request) (*http.Response, error) {
			return nil, context.DeadlineExceeded
		},
	} {
		t.Run(name, func(t *testing.T) {
			tool := NewWebSearch("searxng", "", "https://search.example.org").WithHosted(HostedSearchConfig{Provider: "openai", APIKey: "k"})
			tool.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Host == "api.openai.com" {
					return hostedReply(req)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"results":[{"title":"Local hit","url":"https://example.org/a","content":"from searxng"}]}`)), Request: req}, nil
			})}
			result := searchJSON(t, tool.Spec().Fn, map[string]any{"query": "anything"})
			if result.Err != nil || !strings.Contains(result.Text, "Local hit") || !strings.Contains(result.Text, "(engine: searxng, fallback from openai-hosted)") {
				t.Fatalf("no local fallback: %+v", result)
			}
		})
	}
}

func TestHostedSearchNeedsProviderAndKey(t *testing.T) {
	for _, cfg := range []HostedSearchConfig{{Provider: "openai"}, {Provider: "unknown", APIKey: "k"}, {}} {
		if engine := NewWebSearch("", "").WithHosted(cfg).HostedEngine(); engine != "" {
			t.Errorf("%+v enabled hosted search %q", cfg, engine)
		}
	}
	tool := NewWebSearch("", "").WithHosted(HostedSearchConfig{Provider: "xai", APIKey: "k"})
	if tool.HostedEngine() != "xai-hosted" || tool.hosted.BaseURL != "https://api.x.ai/v1" || tool.hosted.Model == "" {
		t.Fatalf("xai defaults missing: %+v", tool.hosted)
	}
}

func TestWebSearchRunsSeveralQueriesInParallelAndDedupes(t *testing.T) {
	var inFlight, peak int32
	release := make(chan struct{})
	tool := NewWebSearch("searxng", "", "https://search.example.org")
	tool.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		now := atomic.AddInt32(&inFlight, 1)
		for {
			old := atomic.LoadInt32(&peak)
			if now <= old || atomic.CompareAndSwapInt32(&peak, old, now) {
				break
			}
		}
		if now == 3 {
			close(release)
		}
		<-release
		atomic.AddInt32(&inFlight, -1)
		q := req.URL.Query().Get("q")
		body := `{"results":[{"title":"Shared","url":"https://example.org/shared","content":"x"},{"title":"Only ` + q + `","url":"https://example.org/` + strings.ReplaceAll(q, " ", "-") + `","content":"y"}]}`
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})}
	result := searchJSON(t, tool.Spec().Fn, map[string]any{"query": "go release", "queries": []string{"go changelog", "GO RELEASE", "go download"}})
	text := result.Text
	if result.Err != nil || peak != 3 {
		t.Fatalf("queries not run in parallel (peak=%d): %+v", peak, result)
	}
	for _, want := range []string{`Query 1 "go release"`, `Query 2 "go changelog"`, `Query 3 "go download"`, "Only go changelog", "(1 more already listed above)"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Count(text, "https://example.org/shared") != 1 || strings.Contains(text, "Query 4") {
		t.Fatalf("duplicates or repeated query kept:\n%s", text)
	}
	tooMany := searchJSON(t, tool.Spec().Fn, map[string]any{"query": "a", "queries": []string{"b", "c", "d", "e"}})
	if tooMany.Err == nil || !strings.Contains(tooMany.Err.Error(), "at most 4 queries") {
		t.Fatalf("query limit not enforced: %+v", tooMany)
	}
}

func TestExaEngine(t *testing.T) {
	tool := NewWebSearch("exa", "exa-key")
	tool.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		if req.URL.String() != exaEndpoint || req.Header.Get("x-api-key") != "exa-key" || !strings.Contains(string(body), `"includeDomains":["go.dev"]`) || !strings.Contains(string(body), `"startPublishedDate"`) {
			t.Errorf("unexpected exa request %s %s", req.URL, body)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"results":[{"title":"Go 1.27","url":"https://go.dev/blog/go1.27","text":"Go 1.27 adds generic methods.","publishedDate":"2026-08-19T00:00:00Z"}]}`)), Request: req}, nil
	})}
	result := searchJSON(t, tool.Spec().Fn, map[string]any{"query": "go 1.27", "include_domains": []string{"go.dev"}, "freshness": "year"})
	if result.Err != nil || !strings.Contains(result.Text, "(engine: exa)") || !strings.Contains(result.Text, "2026-08-19 — Go 1.27 adds generic methods.") {
		t.Fatalf("exa result wrong: %+v", result)
	}
	if _, err := NewWebSearch("exa", "").searchExa(context.Background(), webSearchArgs{Query: "x", MaxResults: 5}); err == nil || !strings.Contains(err.Error(), "EXA_API_KEY") {
		t.Fatalf("missing key not explained: %v", err)
	}
}
