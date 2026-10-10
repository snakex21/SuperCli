package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWebReadReuseContractsExposeExplicitRefresh(t *testing.T) {
	search := NewWebSearch("", "")
	for _, spec := range []Tool{search.LookupSpec(), search.Spec(), NewWebFetch().Spec()} {
		var schema struct {
			Properties map[string]struct{ Type string }
		}
		if err := json.Unmarshal([]byte(spec.Schema), &schema); err != nil {
			t.Fatalf("%s schema: %v", spec.Name, err)
		}
		if !spec.ReadOnly || spec.ReuseTTL != 2*time.Minute || spec.RefreshArg != "refresh" || schema.Properties["refresh"].Type != "boolean" || !strings.Contains(spec.Description, "refresh=true") {
			t.Errorf("%s does not declare bounded read reuse and explicit refresh: %+v", spec.Name, spec)
		}
	}
	if spec := NewWebDownload(t.TempDir()).Spec(); spec.ReuseTTL != 0 || spec.RefreshArg != "" {
		t.Fatal("file download must not opt into read-result reuse")
	}
}

func TestWebSearchAndLookupRefreshBypassSharedSearchCache(t *testing.T) {
	for _, name := range []string{"web_search", "web_lookup"} {
		t.Run(name, func(t *testing.T) {
			tool := NewWebSearch("searxng", "", "https://search.example.org")
			requests := 0
			tool.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests++
				if req.URL.Path != "/search" || req.URL.Query().Get("q") != "example documentation" {
					t.Fatalf("unexpected search request %s", req.URL)
				}
				return &http.Response{
					StatusCode: http.StatusOK, Header: make(http.Header), Request: req,
					Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"results":[{"title":"Snapshot %d","url":"https://example.org/docs","content":"Revision %d"}]}`, requests, requests))),
				}, nil
			})}
			call := tool.Spec().Fn
			if name == "web_lookup" {
				call = tool.LookupSpec().Fn
			}
			for _, step := range []struct {
				raw      string
				requests int
				revision int
			}{
				{`{"query":"example documentation"}`, 1, 1},
				{`{"query":"example documentation","refresh":false}`, 1, 1},
				{`{"query":"example documentation","refresh":true}`, 2, 2},
				{`{"query":"example documentation"}`, 2, 2},
				{`{"query":"example documentation","refresh":true}`, 3, 3},
			} {
				result, err := call(context.Background(), json.RawMessage(step.raw))
				if err != nil || result.Err != nil || requests != step.requests || !strings.Contains(result.Text, fmt.Sprintf("Revision %d", step.revision)) {
					t.Fatalf("%s: HTTP requests=%d, want %d; result=%+v, err=%v", step.raw, requests, step.requests, result, err)
				}
			}
			// lookup and search share the refreshed lower-layer cache.
			result, err := tool.LookupSpec().Fn(context.Background(), json.RawMessage(`{"query":"example documentation"}`))
			if err != nil || result.Err != nil || requests != 3 || !strings.Contains(result.Text, "Revision 3") {
				t.Fatalf("shared cache did not retain fresh result: requests=%d result=%+v err=%v", requests, result, err)
			}
		})
	}
}

func TestWebSearchRefreshErrorInvalidatesLastSuccessfulResult(t *testing.T) {
	tool := NewWebSearch("searxng", "", "https://search.example.org")
	requests := 0
	tool.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		status, body := http.StatusOK, fmt.Sprintf(`{"results":[{"title":"Snapshot %d","url":"https://example.org/docs","content":"Revision %d"}]}`, requests, requests)
		if requests == 2 {
			status, body = http.StatusUnauthorized, `{"error":"Credentials expired"}`
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Request: req, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	for _, step := range []struct {
		refresh  bool
		requests int
		failed   bool
	}{
		{false, 1, false}, {true, 2, true}, {false, 3, false}, {false, 3, false},
	} {
		result, err := tool.lookup(context.Background(), json.RawMessage(fmt.Sprintf(`{"query":"example docs","refresh":%t}`, step.refresh)))
		if err != nil {
			t.Fatal(err)
		}
		if requests != step.requests {
			t.Fatalf("refresh=%t: HTTP requests=%d, want %d", step.refresh, requests, step.requests)
		}
		if step.failed {
			if result.Err == nil || !strings.Contains(result.Err.Error(), "status=401") {
				t.Fatalf("refresh failure hidden: %+v", result)
			}
		} else if result.Err != nil || !strings.Contains(result.Text, fmt.Sprintf("Revision %d", step.requests)) {
			t.Fatalf("fresh error left stale evidence: %+v", result)
		}
	}
}

func TestWebSearchCanceledRequestNeverReturnsCachedSuccess(t *testing.T) {
	tool := NewWebSearch("", "")
	tool.cache.set(tool.cacheKey(webSearchArgs{Query: "example", MaxResults: webSearchDefaultResults}), searchCacheValue{
		engine: "fixture", results: []WebSearchResult{{Title: "Old result", URL: "https://example.org"}},
	}, time.Hour, time.Now())
	tool.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("canceled request must not reach HTTP transport")
		return nil, nil
	})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, refresh := range []bool{false, true} {
		result, err := tool.lookup(ctx, json.RawMessage(fmt.Sprintf(`{"query":"example","refresh":%t}`, refresh)))
		if err != nil || !errors.Is(result.Err, context.Canceled) || result.Text != "" {
			t.Fatalf("canceled request returned cached success: result=%+v err=%v", result, err)
		}
	}
}

func TestWebSearchOlderHTTPCompletionCannotOverwriteRefresh(t *testing.T) {
	for _, refreshFailed := range []bool{false, true} {
		t.Run(fmt.Sprintf("refresh_failed=%t", refreshFailed), func(t *testing.T) {
			tool := NewWebSearch("searxng", "", "https://search.example.org")
			var requests atomic.Int32
			started, release := make(chan struct{}), make(chan struct{})
			tool.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				n := requests.Add(1)
				if n == 1 {
					close(started)
					select {
					case <-release:
					case <-req.Context().Done():
						return nil, req.Context().Err()
					}
				}
				status, body := http.StatusOK, fmt.Sprintf(`{"results":[{"title":"Snapshot %d","url":"https://example.org/docs","content":"Revision %d"}]}`, n, n)
				if n == 2 && refreshFailed {
					status, body = http.StatusUnauthorized, `{"error":"Refresh denied"}`
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Request: req, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			oldDone := make(chan Result, 1)
			go func() {
				result, _ := tool.lookup(context.Background(), json.RawMessage(`{"query":"example docs"}`))
				oldDone <- result
			}()
			<-started
			fresh, err := tool.lookup(context.Background(), json.RawMessage(`{"query":"example docs","refresh":true}`))
			close(release)
			old := <-oldDone
			if err != nil || (fresh.Err != nil) != refreshFailed || old.Err != nil || !strings.Contains(old.Text, "Revision 1") {
				t.Fatalf("HTTP outcomes: fresh=%+v old=%+v err=%v", fresh, old, err)
			}
			latest, err := tool.lookup(context.Background(), json.RawMessage(`{"query":"example docs"}`))
			wantRequests := int32(2)
			if refreshFailed {
				wantRequests = 3
			}
			if err != nil || latest.Err != nil || requests.Load() != wantRequests || !strings.Contains(latest.Text, fmt.Sprintf("Revision %d", wantRequests)) {
				t.Fatalf("older request repopulated cache: requests=%d latest=%+v err=%v", requests.Load(), latest, err)
			}
		})
	}
}

func TestWebFetchRefreshAcceptsBooleanAndPerformsNetworkRead(t *testing.T) {
	tool := NewWebFetch()
	requests := 0
	tool.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if got := req.URL.String(); got != "https://example.org/docs?signature=a%2Fb&x=1" {
			t.Fatalf("signed URL changed: %s", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/plain"}}, Request: req,
			Body: io.NopCloser(strings.NewReader(fmt.Sprintf("Snapshot %d", requests))),
		}, nil
	})}
	for _, refresh := range []bool{false, true} {
		result, err := tool.Spec().Fn(context.Background(), json.RawMessage(fmt.Sprintf(`{"url":"https://example.org/docs?signature=a%%2Fb&x=1","refresh":%t}`, refresh)))
		if err != nil || result.Err != nil || !strings.Contains(result.Text, fmt.Sprintf("Snapshot %d", requests)) {
			t.Fatalf("fetch refresh=%t: result=%+v err=%v", refresh, result, err)
		}
	}
	if requests != 2 {
		t.Fatalf("HTTP requests=%d, want 2", requests)
	}
	result, err := tool.Spec().Fn(context.Background(), json.RawMessage(`{"url":"https://example.org/docs","refresh":"true"}`))
	if err != nil || result.Err == nil || requests != 2 {
		t.Fatalf("invalid refresh must fail before HTTP: requests=%d result=%+v err=%v", requests, result, err)
	}
}
