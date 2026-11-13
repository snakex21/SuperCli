package web

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// WebFetch fetches a public http(s) URL and returns the page title
// plus readable text content. It is registered opt-in (NOT
// MarkAlwaysOn); the model discovers it via tool_search.
//
// Safety:
//   - http/https only
//   - private, loopback, link-local, and otherwise non-public IPs
//     are blocked at dial time (covers DNS rebinding, not just the
//     initial resolve)
//   - max 5 redirects, 30 s total timeout, 2 MB body cap (20 MB for PDF)
type WebFetch struct {
	client *http.Client
	pages  *pageCache
}

const (
	webFetchTimeout      = 30 * time.Second
	webFetchMaxRedirects = 5
	webFetchMaxBody      = 2 << 20 // 2 MB
	webFetchMaxChars     = 100000
	webFetchMaxPDF       = 20 << 20 // 20 MB
)

// NewWebFetch returns a tool with an SSRF-guarded HTTP client.
func NewWebFetch() *WebFetch {
	return &WebFetch{client: newPublicHTTPClient(webFetchTimeout), pages: newPageCache()}
}

// newPublicHTTPClient is shared by page reads and file downloads. Both check
// every connection at dial time and bound redirects, including cancellation.
func newPublicHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return safeDial(ctx, dialer, network, addr)
		},
		Proxy: http.ProxyFromEnvironment,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= webFetchMaxRedirects {
				return fmt.Errorf("stopped after %d redirects", webFetchMaxRedirects)
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("redirect to non-http(s) URL blocked")
			}
			if req.URL.User != nil {
				return fmt.Errorf("redirect with username/password blocked")
			}
			return nil
		},
	}
}

// safeDial resolves addr and connects only to public IPs. Checking
// at dial time (rather than pre-validating the hostname once)
// closes the DNS-rebinding hole and also covers every redirect hop.
func safeDial(ctx context.Context, d *net.Dialer, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", host, err)
	}
	for _, ip := range ips {
		if blockedIP(ip) {
			return nil, fmt.Errorf("address %s resolves to a private or local IP (%s); fetching internal addresses is not allowed", host, ip)
		}
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("resolve %s: no addresses", host)
	}
	// Dial the first resolved (and verified) IP directly so the
	// connection cannot go to an unchecked address.
	return d.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
}

// blockedIP reports whether ip is not a public unicast address.
func blockedIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() ||
		ip.IsInterfaceLocalMulticast()
}

// validateFetchURL checks the scheme and host shape before any
// network activity. Pure; unit-tested.
func validateFetchURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("only http and https URLs are supported (got %q)", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("URL has no host")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && blockedIP(ip) {
		return nil, fmt.Errorf("fetching private or local addresses is not allowed (%s)", ip)
	}
	return u, nil
}

// isFetchedText prevents compressed files, images and other binary bodies
// from becoming tool-message text. Unknown/octet-stream responses are sniffed
// so plain source files without a useful Content-Type remain readable.
func isFetchedText(prefix []byte, contentType string) bool {
	if bytes.IndexByte(prefix, 0) >= 0 {
		return false
	}
	// Peek may end in the middle of a UTF-8 rune. Accept only that incomplete
	// suffix, never malformed bytes that utf8.FullRune can already classify.
	for rest := prefix; len(rest) > 0; {
		if !utf8.FullRune(rest) {
			break
		}
		r, n := utf8.DecodeRune(rest)
		if r == utf8.RuneError && n == 1 {
			return false
		}
		rest = rest[n:]
	}
	ct := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	sniffed := strings.SplitN(http.DetectContentType(prefix), ";", 2)[0]
	// A misleading text/plain header must not expose a PDF/image/media body.
	if strings.HasPrefix(sniffed, "image/") || strings.HasPrefix(sniffed, "audio/") ||
		strings.HasPrefix(sniffed, "video/") || sniffed == "application/pdf" ||
		sniffed == "application/zip" || sniffed == "application/x-gzip" ||
		sniffed == "application/x-rar-compressed" {
		return false
	}
	if ct == "" || ct == "application/octet-stream" {
		ct = sniffed
	}
	return strings.HasPrefix(ct, "text/") || ct == "application/json" ||
		ct == "application/xml" || ct == "application/xhtml+xml" ||
		ct == "application/javascript" || ct == "image/svg+xml" ||
		strings.HasSuffix(ct, "+json") || strings.HasSuffix(ct, "+xml")
}

// formatFetched converts a response body to (title, readable text)
// based on its content type. Pure; unit-tested.
func formatFetched(body, contentType string) (title, content string) {
	if fetchedHTML(body, contentType) {
		return htmlTitle(body), htmlToText(body)
	}
	return "", strings.TrimSpace(body)
}
