package web

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestWebFetchRejectsBinaryWithoutBodyInContext(t *testing.T) {
	for _, ct := range []string{"image/png", "application/octet-stream", "text/plain"} {
		t.Run(ct, func(t *testing.T) {
			tool := NewWebFetch()
			tool.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{ct}}, Body: io.NopCloser(strings.NewReader("\x89PNG\r\n\x1a\n\x00secret-binary")), Request: req}, nil
			})}
			result, err := tool.Spec().Fn(context.Background(), []byte(`{"url":"https://example.test/asset.png"}`))
			if err != nil || result.Err == nil || !strings.Contains(result.Err.Error(), "web_download") || result.Text != "" || strings.Contains(result.Err.Error(), "secret-binary") {
				t.Fatalf("binary response entered context: %+v err=%v", result, err)
			}
		})
	}
}

func TestFetchedTextSniffingPreservesSourceAndUTF8Boundary(t *testing.T) {
	for _, test := range []struct {
		body, contentType string
		text              bool
	}{
		{"const x = 1;", "application/octet-stream", true},
		{"<!doctype html><html>hello</html>", "", true},
		{"{\"x\":1}", "application/problem+json", true},
		{"<svg>hi</svg>", "image/svg+xml", true},
		{"hi\xe2\x82", "text/plain", true},
		{"hi\xff", "text/plain", false},
		{"PK\x03\x04\x00", "", false},
		{"binary", "application/zip", false},
		{"%PDF-1.7 test", "text/plain", false},
	} {
		if got := isFetchedText([]byte(test.body), test.contentType); got != test.text {
			t.Errorf("isFetchedText(%q,%q)=%v want %v", test.body, test.contentType, got, test.text)
		}
	}
}

func TestWebFetchRejectsBinaryAfterTextualPrefix(t *testing.T) {
	tool := NewWebFetch()
	tool.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/plain"}}, Body: io.NopCloser(strings.NewReader(strings.Repeat("a", 512) + "\x00secret-binary")), Request: req}, nil
	})}
	result, err := tool.Spec().Fn(context.Background(), []byte(`{"url":"https://example.test/file"}`))
	if err != nil || result.Err == nil || result.Text != "" {
		t.Fatalf("late binary reached context: %+v %v", result, err)
	}
}
