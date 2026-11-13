package web

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// RenderReadableHTML renders already-loaded HTML (for example a browser's
// rendered DOM) exactly like web_fetch: main content first, fenced as
// untrusted, paged by offset. withLinks adds the link and structured-data
// footer on the first window; budget is in characters.
func RenderReadableHTML(rawHTML, pageURL string, offset, budget int, withLinks bool) string {
	base, _ := url.Parse(pageURL)
	page := extractReadable(rawHTML, base)
	doc := &fetchedDoc{
		finalURL: pageURL, kind: "html", title: page.Title, description: page.Description,
		text: page.Text, pager: page.Pager, data: page.Data,
	}
	if withLinks {
		doc.links = page.Links
	}
	out := doc.render("text", offset, budget, budget)
	return strings.Replace(out, "web_fetch with the same url and offset=", "the same tool with offset=", 1)
}

// PublicHTTPURL validates an http(s) URL whose host resolves only to public
// addresses, the rule web_fetch applies at dial time. A browser cannot use
// web_fetch's dialer, so its tool checks every top-level address with this.
func PublicHTTPURL(ctx context.Context, raw string) (*url.URL, error) {
	u, err := validateFetchURL(raw)
	if err != nil {
		return nil, err
	}
	host := u.Hostname()
	if net.ParseIP(host) != nil {
		return u, nil
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", host, err)
	}
	for _, ip := range ips {
		if blockedIP(ip) {
			return nil, fmt.Errorf("address %s resolves to a private or local IP (%s); opening internal addresses is not allowed", host, ip)
		}
	}
	return u, nil
}

// FenceUntrusted wraps page-provided text (for example element names from
// a browser snapshot) in the same unpredictable markers web_fetch uses.
func FenceUntrusted(label, body string) string {
	id := fenceID(label, body)
	return "<<<" + label + " " + id + ">>>\n" + body + "<<<end " + label + " " + id + ">>>"
}
