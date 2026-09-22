package llm

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Prove Go can hit 200 when using the proven-good capture session header.
func TestZenGateCapSession(t *testing.T) {
	if !strings.EqualFold(envOr("ZEN_LIVE", ""), "1") {
		t.Skip("set ZEN_LIVE=1")
	}
	body, err := os.ReadFile(`C:\Users\ASRock\Desktop\SuperCli\.tmp-zen-capture\exp_S4_sc_bash_sc_read.json`)
	if err != nil {
		t.Fatal(err)
	}
	// Body has its own prompt_cache_key; we only care about the session header.
	sid := "ses_f35823b5fffe7lXLKSv2dZfIup" // kept for reference; cap session path uses WithOpenCodeSession below
	_ = sid
	base := "https://opencode.ai/zen/v1"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, base+"/responses", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(WithOpenCodeSession(req.Context(), "ses_0123456789abcdef0123456789"))
	ApplyOpenCodeZenHeaders(req, base)

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.ForceAttemptHTTP2 = false
	transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	}
	transport.TLSClientConfig.NextProtos = []string{"http/1.1"}
	client := &http.Client{Transport: transport}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	t.Logf("status=%d headers=%v bodyHead=%s", resp.StatusCode, resp.Header, string(raw[:min(300, len(raw))]))
	if resp.StatusCode != 200 {
		t.Fatalf("go status=%d", resp.StatusCode)
	}
}

// Raw invalid session header (bypasses normalize) must still 403 — proves
// ApplyOpenCodeZenHeaders' normalization is what opens the gate.
func TestZenGateInvalidSessionControl(t *testing.T) {
	if !strings.EqualFold(envOr("ZEN_LIVE", ""), "1") {
		t.Skip("set ZEN_LIVE=1")
	}
	body, err := os.ReadFile(`C:\Users\ASRock\Desktop\SuperCli\.tmp-zen-capture\exp_S4_sc_bash_sc_read.json`)
	if err != nil {
		t.Fatal(err)
	}
	base := "https://opencode.ai/zen/v1"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, base+"/responses", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(WithOpenCodeSession(req.Context(), "ses_livecheck000000000000001"))
	ApplyOpenCodeZenHeaders(req, base)
	// Overwrite after Apply so the wire gets a non-wire-shaped session.
	req.Header.Set("X-OpenCode-Session", "ses_livecheck000000000000001")

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.ForceAttemptHTTP2 = false
	transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	}
	transport.TLSClientConfig.NextProtos = []string{"http/1.1"}
	client := &http.Client{Transport: transport}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	t.Logf("status=%d bodyHead=%s", resp.StatusCode, string(raw[:min(300, len(raw))]))
	if resp.StatusCode != 403 {
		t.Fatalf("expected 403 for raw invalid session, got %d", resp.StatusCode)
	}
}

// Unit: normalizeZenSessionID shape rules.
func TestNormalizeZenSessionID(t *testing.T) {
	valid := "ses_0123456789abcdef0123456789"
	if got := normalizeZenSessionID(valid); got != valid {
		t.Fatalf("valid passthrough: %q", got)
	}
	// Determinism + shape for SuperCli-style IDs.
	a := normalizeZenSessionID("ses_livecheck000000000000001")
	b := normalizeZenSessionID("ses_livecheck000000000000001")
	if a != b {
		t.Fatalf("not deterministic: %q vs %q", a, b)
	}
	if !isZenSessionWireID(a) {
		t.Fatalf("normalized not wire-shaped: %q", a)
	}
	if a == normalizeZenSessionID("other-conversation") && a != normalizeZenSessionID("third") {
		t.Fatal("distinct inputs must map (collision ok only if same)")
	}
	if normalizeZenSessionID("") == "" || !isZenSessionWireID(normalizeZenSessionID("")) {
		t.Fatalf("empty must map to wire shape: %q", normalizeZenSessionID(""))
	}
}
