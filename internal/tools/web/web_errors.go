package web

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	core "supercli/internal/tools/core"
)

// Error-body caps: how much of a non-200 response body we read and
// how much of it the model sees. API error bodies often carry the
// actionable detail ({"code":"missing_api_key",...}) that a bare
// "HTTP 401" hides — dropping them costs blind retry turns.
const (
	errBodyReadMax = 16 << 10 // read at most 16 KB of an error body
	errBodyHead    = 2048     // model sees first 2 KB ...
	errBodyTail    = 2048     // ... plus last 2 KB (4 KB max)
)

// httpFailedErr renders a non-200 response as a structured,
// deterministic error the model can act on in one turn:
//
//	http_failed status=401 host=api.example.com content_type=application/json retry_after=30
//	body:
//	{"code":"missing_api_key","fix":"..."}
//
// First line carries only response facts (status, host, and — when
// present — content type and Retry-After). The body is capped
// head+tail with UTF-8-safe cuts and an omission marker. Request
// headers (Authorization, tokens) are NEVER echoed. Marked
// self-contained so ModelContent does not duplicate it.
func httpFailedErr(resp *http.Response, host string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "http_failed status=%d host=%s", resp.StatusCode, host)
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		if i := strings.IndexByte(ct, ';'); i >= 0 {
			ct = ct[:i]
		}
		fmt.Fprintf(&b, " content_type=%s", strings.TrimSpace(ct))
	}
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		fmt.Fprintf(&b, " retry_after=%s", strings.TrimSpace(ra))
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, errBodyReadMax))
	if text := strings.TrimSpace(string(body)); text != "" {
		b.WriteString("\nbody:\n")
		b.WriteString(core.HeadTail(text, errBodyHead, errBodyTail))
	}
	return core.SelfContainedErr(errors.New(b.String()))
}

// requestFailedErr renders a transport-level failure (no HTTP
// response at all) with a short deterministic cause the model can
// branch on — timeout / dns / tls / canceled — plus the underlying
// error for detail:
//
//	request_failed cause=timeout host=example.com: Get "https://...": context deadline exceeded
func requestFailedErr(err error, host string) error {
	return fmt.Errorf("request_failed cause=%s host=%s: %w", classifyNetErr(err), host, err)
}

// classifyNetErr maps a client.Do error to a short deterministic
// cause token. Only certain facts — anything unrecognized is
// "error".
func classifyNetErr(err error) string {
	var dnsErr *net.DNSError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.As(err, &dnsErr):
		return "dns"
	}
	var (
		certVerify *tls.CertificateVerificationError
		recHdr     tls.RecordHeaderError
		hostname   x509.HostnameError
		unkAuth    x509.UnknownAuthorityError
		certInval  x509.CertificateInvalidError
	)
	if errors.As(err, &certVerify) || errors.As(err, &recHdr) ||
		errors.As(err, &hostname) || errors.As(err, &unkAuth) ||
		errors.As(err, &certInval) {
		return "tls"
	}
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return "timeout"
	}
	return "error"
}
