package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// exaEndpoint is a variable so tests can point it at a fixture transport.
var exaEndpoint = "https://api.exa.ai/search"

// searchExa queries the Exa search API (engine = "exa"; key from
// [web_search] api_key or EXA_API_KEY).
func (t *WebSearch) searchExa(ctx context.Context, a webSearchArgs) ([]WebSearchResult, error) {
	if t.apiKey == "" {
		return nil, fmt.Errorf("exa engine requires an API key ([web_search] api_key in config.toml or EXA_API_KEY)")
	}
	request := map[string]any{
		"query":      a.Query,
		"numResults": a.MaxResults,
		"type":       "auto",
		"contents":   map[string]any{"text": map[string]any{"maxCharacters": 300}},
	}
	if len(a.IncludeDomains) > 0 {
		request["includeDomains"] = a.IncludeDomains
	}
	if len(a.ExcludeDomains) > 0 {
		request["excludeDomains"] = a.ExcludeDomains
	}
	if since := freshnessStart(a.Freshness, time.Now()); since != "" {
		request["startPublishedDate"] = since
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, exaEndpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", t.apiKey)
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, requestFailedErr(err, "api.exa.ai")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, httpFailedErr(resp, "api.exa.ai")
	}
	var parsed struct {
		Results []struct {
			Title         string `json:"title"`
			URL           string `json:"url"`
			Text          string `json:"text"`
			PublishedDate string `json:"publishedDate"`
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
		snippet := collapseSpace(r.Text)
		if r.PublishedDate != "" && len(r.PublishedDate) >= 10 {
			snippet = r.PublishedDate[:10] + " — " + snippet
		}
		out = append(out, WebSearchResult{Title: cleanHTMLFragment(r.Title), URL: r.URL, Snippet: truncateRunes(snippet, 300)})
	}
	return out, nil
}

// freshnessStart converts a day/week/month/year window to an RFC 3339 start.
func freshnessStart(freshness string, now time.Time) string {
	var since time.Time
	switch freshness {
	case "day":
		since = now.AddDate(0, 0, -1)
	case "week":
		since = now.AddDate(0, 0, -7)
	case "month":
		since = now.AddDate(0, -1, 0)
	case "year":
		since = now.AddDate(-1, 0, 0)
	default:
		return ""
	}
	return since.UTC().Format(time.RFC3339)
}
