// Package browser opens validated web addresses in the user's default browser.
package browser

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// NormalizeURL accepts HTTP(S) addresses and supplies a scheme for bare hosts.
// Localhost and IP addresses default to HTTP; other hosts default to HTTPS.
func NormalizeURL(raw string) (string, error) {
	for _, r := range raw {
		if unicode.IsControl(r) || r == '\\' {
			return "", fmt.Errorf("browser URL contains a control character or backslash")
		}
	}
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return "", fmt.Errorf("invalid browser URL escape: %w", err)
	}
	for _, r := range decoded {
		if unicode.IsControl(r) || r == '\\' {
			return "", fmt.Errorf("browser URL contains an escaped control character or backslash")
		}
	}
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", fmt.Errorf("browser URL is empty")
	}

	explicit := false
	if strings.HasPrefix(text, "//") {
		text = text[2:]
	} else {
		authority := text
		if end := strings.IndexAny(authority, "/?#"); end >= 0 {
			authority = authority[:end]
		}
		if strings.HasPrefix(authority, "[") {
			// A bracketed IPv6 host has no scheme; parsing below validates it.
		} else if ip, err := netip.ParseAddr(authority); err == nil && ip.Is6() {
			text = "[" + authority + "]" + text[len(authority):]
		} else if host, _, err := net.SplitHostPort(authority); err == nil && bareHostPort(host) {
			// A dotted host, localhost or IP followed by a port is not a URL scheme.
		} else {
			parsed, err := url.Parse(text)
			if err != nil {
				return "", fmt.Errorf("invalid browser URL: %w", err)
			}
			explicit = parsed.Scheme != ""
		}
	}
	if !explicit {
		text = "https://" + text
	}
	parsed, err := url.Parse(text)
	if err != nil {
		return "", fmt.Errorf("invalid browser URL: %w", err)
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("browser URL requires HTTP or HTTPS")
	}
	if parsed.Opaque != "" || parsed.Host == "" || parsed.Hostname() == "" {
		return "", fmt.Errorf("browser URL requires a host")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("browser URL must not contain credentials")
	}
	host := parsed.Hostname()
	if strings.HasPrefix(parsed.Host, "[") || strings.Contains(host, ":") {
		if ip, err := netip.ParseAddr(host); err != nil || !ip.Is6() || !strings.HasPrefix(parsed.Host, "[") {
			return "", fmt.Errorf("browser URL has an invalid IP address")
		}
	}
	if strings.HasSuffix(parsed.Host, ":") {
		return "", fmt.Errorf("browser URL has an empty port")
	}
	if port := parsed.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("browser URL has an invalid port")
		}
	}
	if !explicit && localOrIP(host) {
		parsed.Scheme = "http"
	}
	return parsed.String(), nil
}

func bareHostPort(host string) bool {
	return localOrIP(host) || strings.Contains(host, ".")
}

func localOrIP(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" {
		return true
	}
	_, err := netip.ParseAddr(host)
	return err == nil
}

// Open validates raw and asks the operating system to open it in the default
// browser. It returns launch failures; it does not wait for a browser window.
func Open(raw string) error {
	normalized, err := NormalizeURL(raw)
	if err != nil {
		return err
	}
	return openURL(normalized)
}
