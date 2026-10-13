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
	// Genuine wire shape: 12 hex with low12=0xFFF (counter 0) + 14 base62.
	valid := "ses_cd67f91c1fffnakl7eqdkWXvHA"
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

// TestZenSessionWireIDCounter pins the free-tier counter bound: the
// descending-encoded value's low 12 bits must be ≥ 0xFC0 (counter ≤ 63).
// Bisect 2026-10-13: counter 63 → 200, counter 64 → 403.
func TestZenSessionWireIDCounter(t *testing.T) {
	// Last 3 hex digits of the 12-hex time part carry low 12 bits:
	// counter = 0xFFF - low12, so fff→0, ffe→1, fc0→63, fbf→64, f00→255, 9ab→1620.
	cases := []struct {
		id   string
		want bool
	}{
		{"ses_ffffffffffffabcdefghijklmn", true},  // low12=fff, counter 0
		{"ses_fffffffffffeabcdefghijklmn", true},  // low12=ffe, counter 1
		{"ses_ffffffffffc0abcdefghijklmn", true},  // low12=fc0, counter 63
		{"ses_ffffffffffbfabcdefghijklmn", false}, // low12=fbf, counter 64
		{"ses_ffffffffff00abcdefghijklmn", false}, // low12=f00, counter 255
		{"ses_fffffffff9abcdefghijklmn", false},   // low12=9ab, counter 1620
		// Shape failures: short hex, bad suffix charset.
		{"ses_ffffabcdefghijklmn", false},
		{"ses_ffffffffffff!!!!!!!!!!!!!!", false},
	}
	for _, c := range cases {
		if got := isZenSessionWireID(c.id); got != c.want {
			t.Errorf("isZenSessionWireID(%q) = %v, want %v", c.id, got, c.want)
		}
	}
}

// TestZenSessionFromEntropyRecency: minted IDs must carry a real wall-clock
// millisecond (not hash-derived garbage) so a future recency check on the
// free-tier edge cannot reject them. Determinism still holds within a process.
func TestZenSessionFromEntropyRecency(t *testing.T) {
	id := zenSessionFromEntropy("recency-test-entropy")
	if !isZenSessionWireID(id) {
		t.Fatalf("not wire-shaped: %q", id)
	}
	// The 48-bit field stores ~(ms*4096+counter) mod 2^48; current ms overflows
	// 48 bits so only ms mod 2^36 is recoverable. Check it tracks wall-clock.
	var v uint64
	for _, c := range id[4:16] {
		v = v<<4 | hexVal(c)
	}
	msMod := ((^v) & 0xFFFFFFFFFFFF) >> 12
	nowMod := uint64(time.Now().UnixMilli()) & 0xFFFFFFFFF // 2^36-1
	diff := int64(msMod) - int64(nowMod)
	if diff < 0 {
		diff = -diff
	}
	// Allow wrap-around near the 2^36 boundary (≈ every 2.18 years).
	if diff > 10000 && diff < (1<<36)-10000 {
		t.Fatalf("minted ms mod 2^36 = %d not near now mod 2^36 = %d (id=%q)", msMod, nowMod, id)
	}
	// Same entropy must stay stable (cache).
	if again := zenSessionFromEntropy("recency-test-entropy"); again != id {
		t.Fatalf("cache miss: %q vs %q", id, again)
	}
}
