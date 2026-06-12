package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
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
//   - max 5 redirects, 30 s total timeout, 2 MB body cap
type WebFetch struct {
	client *http.Client
}

const (
	webFetchTimeout      = 30 * time.Second
	webFetchMaxRedirects = 5
	webFetchMaxBody      = 2 << 20 // 2 MB
	webFetchDefaultChars = 20000
	webFetchMaxChars     = 100000
)

// NewWebFetch returns a tool with an SSRF-guarded HTTP client.
func NewWebFetch() *WebFetch {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return safeDial(ctx, dialer, network, addr)
		},
		Proxy: http.ProxyFromEnvironment,
	}
	return &WebFetch{
		client: &http.Client{
			Timeout:   webFetchTimeout,
			Transport: transport,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= webFetchMaxRedirects {
					return fmt.Errorf("stopped after %d redirects", webFetchMaxRedirects)
				}
				if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
					return fmt.Errorf("redirect to non-http(s) URL blocked: %s", req.URL)
				}
				return nil
			},
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

type webFetchArgs struct {
	URL      string `json:"url"`
	MaxChars int    `json:"max_chars"`
}

// Spec returns the tool registration.
func (t *WebFetch) Spec() Tool {
	return Tool{
		Name: "web_fetch",
		Description: "Fetch a public web page by URL and return its title and readable text content (HTML is converted to plain text; scripts/styles/navigation stripped). " +
			"Use when the user gives a URL, or after web_search to read a result in full. " +
			"Do not use for local files (use read_lines/file tools), internal/private addresses (blocked), or APIs requiring authentication. Read-only; never submits forms.",
		Schema: `{
  "type": "object",
  "properties": {
    "url":       {"type": "string", "description": "Full http(s) URL to fetch."},
    "max_chars": {"type": "integer", "description": "Truncate returned content to this many characters (default 20000, max 100000)."}
  },
  "required": ["url"]
}`,
		Fn: t.execute,
	}
}

func (t *WebFetch) execute(ctx context.Context, args json.RawMessage) (Result, error) {
	var a webFetchArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("web_fetch: bad args: %w", err)}, nil
	}
	u, err := validateFetchURL(a.URL)
	if err != nil {
		return Result{Err: fmt.Errorf("web_fetch: %w", err)}, nil
	}
	maxChars := a.MaxChars
	if maxChars <= 0 {
		maxChars = webFetchDefaultChars
	}
	if maxChars > webFetchMaxChars {
		maxChars = webFetchMaxChars
	}

	ctx, cancel := context.WithTimeout(ctx, webFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Result{Err: fmt.Errorf("web_fetch: %w", err)}, nil
	}
	req.Header.Set("User-Agent", "SuperCli/1.0 (web_fetch tool)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain,*/*")

	resp, err := t.client.Do(req)
	if err != nil {
		return Result{Err: fmt.Errorf("web_fetch: %w", err)}, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{Err: fmt.Errorf("web_fetch: %s returned HTTP %d", u.Host, resp.StatusCode)}, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, webFetchMaxBody))
	if err != nil {
		return Result{Err: fmt.Errorf("web_fetch: read body: %w", err)}, nil
	}

	contentType := resp.Header.Get("Content-Type")
	title, content := formatFetched(string(body), contentType)

	finalURL := resp.Request.URL.String()
	var sb strings.Builder
	fmt.Fprintf(&sb, "URL: %s\n", finalURL)
	if title != "" {
		fmt.Fprintf(&sb, "Title: %s\n", title)
	}
	sb.WriteString("\n")
	if len(content) > maxChars {
		sb.WriteString(content[:maxChars])
		fmt.Fprintf(&sb, "\n\n[content truncated at %d characters; total %d]", maxChars, len(content))
	} else {
		sb.WriteString(content)
	}
	return Result{Text: sb.String()}, nil
}

// formatFetched converts a response body to (title, readable text)
// based on its content type. Pure; unit-tested.
func formatFetched(body, contentType string) (title, content string) {
	ct := strings.ToLower(contentType)
	isHTML := strings.Contains(ct, "text/html") || strings.Contains(ct, "application/xhtml")
	// Sniff when the server didn't say: leading '<' + html/doctype.
	if ct == "" || strings.Contains(ct, "octet-stream") {
		trimmed := strings.TrimSpace(body)
		low := strings.ToLower(trimmed)
		if strings.HasPrefix(low, "<!doctype html") || strings.HasPrefix(low, "<html") {
			isHTML = true
		}
	}
	if isHTML {
		return htmlTitle(body), htmlToText(body)
	}
	return "", strings.TrimSpace(body)
}
