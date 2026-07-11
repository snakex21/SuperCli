package web

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
)

// httpFailedErr must surface the response facts and a capped body —
// the {"code":"missing_api_key"} class of detail that a bare
// "HTTP 401" hides.
func TestHTTPFailedErr_StatusAndBody(t *testing.T) {
	resp := &http.Response{
		StatusCode: 401,
		Header: http.Header{
			"Content-Type": []string{"application/json; charset=utf-8"},
			"Retry-After":  []string{"30"},
		},
		Body: io.NopCloser(strings.NewReader(`{"code":"missing_api_key","fix":"set BRAVE_API_KEY"}`)),
	}
	err := httpFailedErr(resp, "api.example.com")
	msg := err.Error()
	if !strings.HasPrefix(msg, "http_failed status=401 host=api.example.com content_type=application/json retry_after=30") {
		t.Fatalf("first line wrong: %q", msg)
	}
	if !strings.Contains(msg, "missing_api_key") {
		t.Fatalf("body dropped: %q", msg)
	}
}

func TestHTTPFailedErr_HugeBodyCapped(t *testing.T) {
	body := strings.Repeat("x", 100_000)
	resp := &http.Response{
		StatusCode: 500,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	msg := httpFailedErr(resp, "h").Error()
	if len(msg) > errBodyHead+errBodyTail+256 {
		t.Fatalf("error not capped: %d bytes", len(msg))
	}
	if !strings.Contains(msg, "omitted_bytes=") {
		t.Fatalf("truncation marker missing: %q", msg)
	}
}

func TestHTTPFailedErr_EmptyBody(t *testing.T) {
	resp := &http.Response{
		StatusCode: 404,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("")),
	}
	msg := httpFailedErr(resp, "h").Error()
	if strings.Contains(msg, "body:") {
		t.Fatalf("empty body should be omitted: %q", msg)
	}
}

func TestClassifyNetErr(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{context.DeadlineExceeded, "timeout"},
		{context.Canceled, "canceled"},
		{&net.DNSError{Err: "no such host", Name: "x"}, "dns"},
		{io.ErrUnexpectedEOF, "error"},
	}
	for _, c := range cases {
		if got := classifyNetErr(c.err); got != c.want {
			t.Errorf("classifyNetErr(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}
