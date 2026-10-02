package browser

import (
	"strings"
	"testing"
)

func TestNormalizeURL(t *testing.T) {
	tests := []struct{ raw, want string }{
		{"example.com", "https://example.com"},
		{"example.com:8443/docs", "https://example.com:8443/docs"},
		{"example.com/docs?q=hello&lang=pl#start", "https://example.com/docs?q=hello&lang=pl#start"},
		{"  https://example.com/path  ", "https://example.com/path"},
		{"HTTP://example.com/path", "http://example.com/path"},
		{"http://example.com", "http://example.com"},
		{"https://localhost:3000", "https://localhost:3000"},
		{"localhost", "http://localhost"},
		{"LOCALHOST:3000/path", "http://LOCALHOST:3000/path"},
		{"localhost.:3000", "http://localhost.:3000"},
		{"127.0.0.1:8080", "http://127.0.0.1:8080"},
		{"192.168.1.5", "http://192.168.1.5"},
		{"8.8.8.8", "http://8.8.8.8"},
		{"[::1]:3000/path", "http://[::1]:3000/path"},
		{"[2001:db8::1]", "http://[2001:db8::1]"},
		{"::1", "http://[::1]"},
		{"2001:db8::1/path", "http://[2001:db8::1]/path"},
		{"http://[fe80::1%25eth0]/", "http://[fe80::1%25eth0]/"},
		{"//example.com/path", "https://example.com/path"},
		{"//localhost:3000/path", "http://localhost:3000/path"},
		{"https://example.com:65535/a b", "https://example.com:65535/a%20b"},
		{"example.com/?q=a;b&value=percent%25", "https://example.com/?q=a;b&value=percent%25"},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := NormalizeURL(tt.raw)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
			again, err := NormalizeURL(got)
			if err != nil || again != got {
				t.Fatalf("normalization is not stable: %q, %v", again, err)
			}
		})
	}
}

func TestNormalizeURLRejectsInvalidAddresses(t *testing.T) {
	tests := []string{
		"", "   ", "https://", "https:///path", "http:example.com", "https:/example.com", "http://?q=x", "http://#fragment",
		"https://user@example.com", "https://user:pass@example.com", "https://%75ser@example.com", "user@example.com",
		"javascript:alert(1)", "javascript:80", "data:text/html,hello", "data:80", "file:///C:/page.html", "mailto:test@example.com", "ftp://example.com", "custom:123", "custom://example.com",
		"localhost:0", "localhost:65536", "localhost:-1", "localhost:not-a-port", "localhost:",
		"https://example.com:", "https://example.com:0", "https://example.com:65536", "https://example.com:abc", "https://example.com:-80", "https://example.com:1.5",
		"http://[::1", "http://[example.com]", "http://[127.0.0.1]", "http://[bad::address]", "http://::1", ":8080", "http://:8080",
		"https://bad host.example", "https://example.com/%zz", "https://example.com/%00", "https://example.com/?q=%0a", "https://example.com/%5c",
		"https://example.com\\path", "C:\\page.html", "https://example.com/\x00", "https://example.com/\npath", "\thttps://example.com", "https://example.com\r", "https://example.com/\x7f", "https://example.com/\u0085",
	}
	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			got, err := NormalizeURL(raw)
			if err == nil || got != "" {
				t.Fatalf("accepted %q as %q (%v)", raw, got, err)
			}
		})
	}
}

func TestOpenRejectsInvalidURLBeforeLaunching(t *testing.T) {
	// All these fail normalization, so the tests never invoke an OS launcher.
	for _, raw := range []string{"javascript:alert(1)", "https://user@example.com", "https://example.com\x00", "https://example.com\\path"} {
		if err := Open(raw); err == nil {
			t.Fatalf("Open accepted %q", raw)
		}
	}
}

func FuzzNormalizeURL(f *testing.F) {
	for _, seed := range []string{"example.com", "localhost:3000", "[::1]:8080/path", "javascript:alert(1)", "https://user@example.com", "https://example.com/?a=1&b=2"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		normalized, err := NormalizeURL(raw)
		if err != nil {
			return
		}
		if !strings.HasPrefix(normalized, "http://") && !strings.HasPrefix(normalized, "https://") {
			t.Fatalf("unsupported normalized scheme: %q", normalized)
		}
		again, err := NormalizeURL(normalized)
		if err != nil || again != normalized {
			t.Fatalf("unstable normalization: %q -> %q (%v)", normalized, again, err)
		}
	})
}
