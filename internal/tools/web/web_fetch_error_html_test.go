package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestWebFetchHTML404ReturnsReadableFailureWithoutBootstrapData(t *testing.T) {
	page := `<!doctype html><html><head><!-- <title>COMMENT-SECRET</title> --><script id="data" type="text/x-cache">eyJCT09UU1RSQV9QU0VDUkVUIjoiRE9OT1RSRUFELUlORk8ifQ==</script><script>const hidden = "<title>SCRIPT-SECRET</title>";</script><title>Page not found</title><meta name="config" content="META-SECRET"></head><body><nav>UNRELATED-NAV</nav><div data-config="ATTRIBUTE-SECRET>QUOTED-SECRET"></div><h1>404: This page is not available</h1><p>Choose another existing page.</p><template>PRIVATE-TEMPLATE<template>PRIVATE-INNER</template>PRIVATE-OUTER</template><svg/><script>UNCLOSED-BOOTSTRAP ` + strings.Repeat("eyJQUklWQVRFX0NPTkZJRyI6InNlY3JldCJ9", errBodyReadMax) + `</script></body></html>`
	for _, mode := range []string{"media", "text", ""} {
		for _, ct := range []string{"text/html; charset=utf-8", "application/xhtml+xml", "", "text/plain"} {
			t.Run(mode+"/"+ct, func(t *testing.T) {
				calls := 0
				stream := &countingMediaBody{Reader: strings.NewReader(page)}
				tool := &WebFetch{client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					calls++
					return &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{"Content-Type": []string{ct}}, Body: stream, Request: req}, nil
				})}}
				args, _ := json.Marshal(webFetchArgs{URL: "https://example.org/missing-page", Mode: mode})
				result, err := tool.Spec().Fn(context.Background(), args)
				if err != nil || result.Err == nil || calls != 1 || !stream.closed || stream.read > errBodyReadMax {
					t.Fatalf("HTML failure must use one bounded request: err=%v result=%+v calls=%d closed=%v read=%d", err, result, calls, stream.closed, stream.read)
				}
				message := result.Err.Error()
				for _, required := range []string{"status=404", "host=example.org", "page_title: Page not found", "404: This page is not available", "Choose another existing page.", "web_lookup", "then web_fetch"} {
					if !strings.Contains(message, required) {
						t.Errorf("missing %q in %q", required, message)
					}
				}
				for _, omitted := range []string{"SECRET", "eyJ", "UNCLOSED-BOOTSTRAP", "PRIVATE-", "UNRELATED-NAV", "<", "<script", "body:\n"} {
					if strings.Contains(message, omitted) {
						t.Errorf("hidden/raw HTML data %q exposed: %q", omitted, message)
					}
				}
				if len(message) > 700 || result.Text != "" {
					t.Fatalf("HTML failure should be compact error evidence: %+v", result)
				}
			})
		}
	}
}

func TestWebFetchAPIErrorsKeepPlainAndJSONDiagnostics(t *testing.T) {
	for _, fixture := range []struct{ contentType, body string }{
		{"application/json", `{"code":"missing_api_key","fix":"configure the documented credential","example":"<script>literal JSON field</script>"}`},
		{"text/plain", "quota exceeded: retry after 60 seconds"},
	} {
		tool := &WebFetch{client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusUnauthorized, Header: http.Header{"Content-Type": []string{fixture.contentType}}, Body: io.NopCloser(strings.NewReader(fixture.body)), Request: req}, nil
		})}}
		result, err := tool.Spec().Fn(context.Background(), []byte(`{"url":"https://api.example.org/endpoint","mode":"media"}`))
		if err != nil || result.Err == nil || !strings.Contains(result.Err.Error(), fixture.body) || !strings.Contains(result.Err.Error(), "status=401") || strings.Contains(result.Err.Error(), "page_title:") {
			t.Fatalf("API diagnostic changed: %s => %+v err=%v", fixture.contentType, result, err)
		}
	}
}
