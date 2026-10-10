package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Searching references to a URL is legitimate. The request must stay a search
// on the selected engine, while the result accurately labels the evidence.
func TestURLReferenceQueriesStaySearchesWithClearEvidence(t *testing.T) {
	for _, query := range []string{
		"https://gallery.arbitrary.example/view/42?size=large&lang=pl",
		"https://storage.other.example/assets/a.gif",
		"https://storage.other.example/asset?sig=a%2Bb%2Fc%3D&part=1&part=2",
		"references to https://new-source.example/document.pdf",
		"site:new-source.example resource documentation",
	} {
		t.Run(query, func(t *testing.T) {
			for _, name := range []string{"web_lookup", "web_search"} {
				t.Run(name, func(t *testing.T) {
					search := NewWebSearch("searxng", "", "https://search.example.com")
					requests := 0
					search.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
						requests++
						if req.URL.Host != "search.example.com" || req.URL.Query().Get("q") != query {
							t.Fatalf("query was redirected or rewritten: %s", req.URL.String())
						}
						return &http.Response{
							StatusCode: http.StatusOK, Header: make(http.Header), Request: req,
							Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"results":[{"title":"Reference page","url":"https://citation.example/reference","content":"External citation revision %d."}]}`, requests))),
						}, nil
					})}
					call := search.LookupSpec().Fn
					if name == "web_search" {
						call = search.Spec().Fn
					}
					for _, step := range []struct {
						refresh  bool
						requests int
					}{{false, 1}, {false, 1}, {true, 2}} {
						raw, _ := json.Marshal(map[string]any{"query": query, "refresh": step.refresh})
						result, err := call(context.Background(), raw)
						if err != nil || result.Err != nil || requests != step.requests {
							t.Fatalf("result=%+v error=%v requests=%d want=%d", result, err, requests, step.requests)
						}
						if !strings.Contains(result.Text, query) || !strings.Contains(result.Text, "https://citation.example/reference") || !strings.Contains(result.Text, fmt.Sprintf("External citation revision %d.", step.requests)) {
							t.Fatal("search evidence or original query was lost")
						}
						wantClarification := strings.HasPrefix(query, "https://")
						if strings.Contains(result.Text, urlSearchResultClarification) != wantClarification {
							t.Fatal("URL-query evidence clarification missing or added to ordinary terms")
						}
					}
				})
			}
		})
	}
}

func TestSearchContractsDistinguishKnownURLActionsFromReferences(t *testing.T) {
	search := NewWebSearch("", "")
	queryDescription := ""
	for _, spec := range []Tool{search.LookupSpec(), search.Spec()} {
		var schema struct {
			Properties map[string]struct{ Description string }
		}
		if err := json.Unmarshal([]byte(spec.Schema), &schema); err != nil {
			t.Fatal(err)
		}
		description := schema.Properties["query"].Description
		for _, cue := range []string{"external mentions or references", "web_fetch", "web_download"} {
			if !strings.Contains(description, cue) {
				t.Fatalf("%s query contract omits %q: %s", spec.Name, cue, description)
			}
		}
		if queryDescription != "" && description != queryDescription {
			t.Fatal("lookup and search describe URL-query intent differently")
		}
		queryDescription = description
		if !strings.Contains(spec.Description, "external references") || !strings.Contains(spec.Description, "without another search") {
			t.Fatalf("%s does not distinguish references from reading/saving known URLs", spec.Name)
		}
	}
}

func TestURLReferenceQueryWithoutResultsStillExplainsEvidence(t *testing.T) {
	query := "https://unknown.example/view/42"
	text := formatSearchResults(query, "fixture", "", nil)
	if !strings.Contains(text, query) || !strings.Contains(text, "No results") || !strings.Contains(text, urlSearchResultClarification) {
		t.Fatalf("empty search result changed query or omitted clarification: %s", text)
	}
}
