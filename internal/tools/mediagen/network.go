package mediagen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

func strictURL(raw string, allowTestHTTP bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("invalid provider or media URL")
	}
	if u.Scheme != "https" && !(allowTestHTTP && u.Scheme == "http") {
		return nil, errors.New("media URLs require HTTPS")
	}
	if err := validateHostname(u.Hostname()); err != nil {
		return nil, err
	}
	if !allowTestHTTP {
		if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !publicIP(ip) {
			return nil, errors.New("private or special-use network addresses are forbidden")
		}
	}
	return u, nil
}
func validateHostname(host string) error {
	host = strings.ToLower(host)
	if host == "" || strings.ContainsAny(host, "%\\\x00\r\n") || strings.HasSuffix(host, ".") {
		return errors.New("invalid hostname")
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.Zone() != "" {
			return errors.New("scoped network addresses are forbidden")
		}
		return nil
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return errors.New("private hostnames are forbidden")
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return errors.New("invalid hostname")
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return errors.New("invalid hostname")
			}
		}
	}
	return nil
}

var specialNetworks = []netip.Prefix{
	netip.MustParsePrefix("fec0::/10"), netip.MustParsePrefix("::/96"),
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"), netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
}

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.Zone() != "" || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range specialNetworks {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

// Resolve and validate every address, then dial the validated IP directly.
// This pins DNS for the actual socket and avoids a check-then-resolve SSRF gap.
func safeDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("invalid dial address")
	}
	if err = validateHostname(host); err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, errors.New("provider DNS lookup failed")
	}
	if len(addresses) == 0 {
		return nil, errors.New("provider DNS returned no addresses")
	}
	for _, ip := range addresses {
		if !publicIP(ip) {
			return nil, errors.New("private or special-use network address rejected")
		}
	}
	dialer := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	for _, ip := range addresses {
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		err = dialErr
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	_ = err
	return nil, errors.New("provider connection failed")
}
func newHTTPClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy: nil, DialContext: safeDial, ForceAttemptHTTP2: true,
		MaxIdleConns: 2, MaxIdleConnsPerHost: 2, MaxConnsPerHost: 2,
		IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}}
}

func origin(u *url.URL) string {
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return strings.ToLower(u.Scheme + "://" + net.JoinHostPort(u.Hostname(), port))
}
func (t *Tool) authenticatedURL(rc runtimeConfig, raw string) (string, error) {
	u, err := strictURL(raw, t.allowTestHTTP)
	if err != nil || origin(u) != origin(rc.base) {
		return "", errors.New("provider returned an untrusted authenticated URL")
	}
	return u.String(), nil
}
func (t *Tool) downloadURL(rc runtimeConfig, raw string) (string, error) {
	u, err := strictURL(raw, t.allowTestHTTP)
	if err != nil {
		return "", errors.New("provider returned an invalid media download URL")
	}
	if !rc.hosts[strings.ToLower(u.Hostname())] {
		return "", errors.New("media download host is not explicitly allowed")
	}
	if u.Port() != "" && u.Port() != "443" && !t.allowTestHTTP {
		return "", errors.New("media downloads require the standard HTTPS port")
	}
	return u.String(), nil
}

func requestJSON(ctx context.Context, client *http.Client, method, endpoint, auth string, payload any, limit int64, out any) error {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return errors.New("cannot encode provider request")
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return errors.New("invalid provider request")
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("provider request failed (no automatic retry)")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("provider returned HTTP %d (response body withheld; no automatic retry)", resp.StatusCode)
	}
	data, err := readBounded(ctx, resp.Body, resp.ContentLength, limit)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err = json.Unmarshal(data, out); err != nil {
		return errors.New("provider returned invalid JSON")
	}
	return nil
}
func readBounded(ctx context.Context, r io.Reader, length, limit int64) ([]byte, error) {
	if length > limit {
		return nil, errors.New("provider response exceeds byte limit")
	}
	data, err := io.ReadAll(io.LimitReader(&contextReader{ctx: ctx, reader: r}, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("provider response exceeds byte limit")
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return data, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
