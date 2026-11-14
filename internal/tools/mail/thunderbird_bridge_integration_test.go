package mail

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Integration tests run the real bridge handler (transport guard included)
// on a loopback test server. tbTestClient is an independent Go implementation
// of the extension side; extension/bridge-auth.js is checked against the same
// host by TestThunderbirdDeliveryExtensionLostACKE2E and the shared vectors.

const tbTestOrigin = "moz-extension://5f3c2a10-0000-4000-8000-000000000001"

type tbTestClient struct {
	t         *testing.T
	base      string
	origin    string
	hostID    string
	pairingID string
	key       []byte
	session   string
	sessKey   []byte
}

func newTBTestServer(t *testing.T) (*thunderbirdBridgeState, *httptest.Server) {
	t.Helper()
	b := &thunderbirdBridgeState{
		queue:        make(chan thunderbirdBridgeRequest, 32),
		waiters:      make(map[string]chan thunderbirdBridgeResponse),
		transfers:    make(map[string]thunderbirdFileTransfer),
		downloads:    make(map[string]thunderbirdDownloadedAttachment),
		dataDir:      t.TempDir(),
		testNoListen: true,
	}
	server := httptest.NewServer(b.handler())
	t.Cleanup(server.Close)
	b.addr = strings.TrimPrefix(server.URL, "http://")
	return b, server
}

func (c *tbTestClient) raw(method, path string, body []byte, header http.Header) (*http.Response, []byte) {
	c.t.Helper()
	req, err := http.NewRequest(method, c.base+path, bytes.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	if c.origin != "" {
		req.Header.Set("Origin", c.origin)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, data
}

func (c *tbTestClient) postJSON(path string, value any) (*http.Response, map[string]any) {
	c.t.Helper()
	body, _ := json.Marshal(value)
	resp, data := c.raw(http.MethodPost, path, body, http.Header{"Content-Type": {"application/json"}})
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	return resp, out
}

func (c *tbTestClient) hello() thunderbirdHostInfo {
	c.t.Helper()
	resp, data := c.raw(http.MethodGet, "/v2/hello", nil, nil)
	var info thunderbirdHostInfo
	if resp.StatusCode != http.StatusOK || json.Unmarshal(data, &info) != nil {
		c.t.Fatalf("hello: %d %s", resp.StatusCode, data)
	}
	return info
}

// pair runs the full pairing exchange and verifies both host proofs.
func (c *tbTestClient) pair(code string) error {
	c.t.Helper()
	normalized, ok := thunderbirdNormalizePairingCode(code)
	if !ok {
		return errors.New("bad code")
	}
	codeKey := thunderbirdCodeKey(normalized)
	info := c.hello()
	priv, _ := ecdh.P256().GenerateKey(rand.Reader)
	extPub := b64.EncodeToString(priv.PublicKey().Bytes())
	extNonce := thunderbirdRandomID(32)
	resp, start := c.postJSON("/v2/pair/start", map[string]any{
		"proto": 2, "extPub": extPub, "extNonce": extNonce, "extVersion": "test",
		"mac": thunderbirdMAC64(codeKey, "pair-start", extPub, extNonce),
	})
	if resp.StatusCode != http.StatusOK {
		return errors.New(start["error"].(string))
	}
	hostPubRaw, _ := b64.DecodeString(start["hostPub"].(string))
	hostPub, err := ecdh.P256().NewPublicKey(hostPubRaw)
	if err != nil {
		return err
	}
	shared, _ := priv.ECDH(hostPub)
	transcript := thunderbirdPairTranscript(info.HostID, extPub, start["hostPub"].(string), extNonce, start["hostNonce"].(string))
	key, _ := thunderbirdPairingKey(shared, codeKey, transcript)
	if !thunderbirdVerifyMAC(key, start["mac"].(string), "pair-host", transcript) {
		return errors.New("host pairing proof invalid")
	}
	resp, finish := c.postJSON("/v2/pair/finish", map[string]any{"hostId": info.HostID, "mac": thunderbirdMAC64(key, "pair-ext", transcript)})
	if resp.StatusCode != http.StatusOK {
		return errors.New(finish["error"].(string))
	}
	pairingID := finish["pairingId"].(string)
	if !thunderbirdVerifyMAC(key, finish["mac"].(string), "pair-done", transcript, pairingID) {
		return errors.New("host final proof invalid")
	}
	c.hostID, c.pairingID, c.key = info.HostID, pairingID, key
	return nil
}

// auth authenticates and verifies the host's proof; it returns the host's
// error code on refusal.
func (c *tbTestClient) auth() string {
	c.t.Helper()
	info := c.hello()
	extNonce := thunderbirdRandomID(32)
	resp, out := c.postJSON("/v2/auth", map[string]any{
		"hostId": c.hostID, "pairingId": c.pairingID, "hostNonce": info.Challenge, "extNonce": extNonce,
		"mac": thunderbirdMAC64(c.key, "auth-ext", c.hostID, c.pairingID, info.Challenge, extNonce),
	})
	if resp.StatusCode != http.StatusOK {
		code, _ := out["error"].(string)
		return code
	}
	session := out["sessionId"].(string)
	if !thunderbirdVerifyMAC(c.key, out["mac"].(string), "auth-host", c.hostID, c.pairingID, info.Challenge, extNonce, session) {
		c.t.Fatal("host authentication proof invalid")
	}
	c.session = session
	c.sessKey = thunderbirdSessionKey(c.key, info.Challenge, extNonce, session)
	return ""
}

type tbSigned struct {
	header http.Header
	nonce  string
}

func (c *tbTestClient) sign(method, path string, body []byte) tbSigned {
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	nonce := thunderbirdRandomID(16)
	h := http.Header{}
	h.Set(headerSession, c.session)
	h.Set(headerTS, ts)
	h.Set(headerNonce, nonce)
	h.Set(headerMAC, thunderbirdMAC64(c.sessKey, thunderbirdRequestMACFields(method, path, ts, nonce, thunderbirdBodyHash(body))...))
	return tbSigned{h, nonce}
}

// signed sends one signed request and verifies the response signature.
func (c *tbTestClient) signed(method, path string, body []byte) (int, []byte) {
	c.t.Helper()
	s := c.sign(method, path, body)
	resp, data := c.raw(method, path, body, s.header)
	if resp.StatusCode != http.StatusUnauthorized {
		ts := resp.Header.Get(headerTS)
		if !thunderbirdVerifyMAC(c.sessKey, resp.Header.Get(headerMAC), thunderbirdResponseMACFields(resp.StatusCode, s.nonce, ts, thunderbirdBodyHash(data))...) {
			c.t.Fatalf("%s %s: response signature invalid (status %d)", method, path, resp.StatusCode)
		}
	}
	return resp.StatusCode, data
}

func pairedTBClient(t *testing.T, b *thunderbirdBridgeState, server *httptest.Server) *tbTestClient {
	t.Helper()
	code, err := b.startPairing()
	if err != nil {
		t.Fatal(err)
	}
	c := &tbTestClient{t: t, base: server.URL, origin: tbTestOrigin}
	if err := c.pair(code.Code); err != nil {
		t.Fatalf("pair: %v", err)
	}
	if reason := c.auth(); reason != "" {
		t.Fatalf("auth: %s", reason)
	}
	return c
}

func TestThunderbirdBridgeHappyPathPairAuthPollResult(t *testing.T) {
	b, server := newTBTestServer(t)
	c := pairedTBClient(t, b, server)

	done := make(chan error, 1)
	var data json.RawMessage
	go func() {
		var err error
		data, err = b.call(context.Background(), "count", json.RawMessage(`{"op":"count"}`))
		done <- err
	}()
	status, body := c.signed(http.MethodGet, "/v2/poll", nil)
	if status != http.StatusOK {
		t.Fatalf("poll status %d %s", status, body)
	}
	var req thunderbirdBridgeRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Op != "count" {
		t.Fatalf("poll body %s", body)
	}
	result, _ := json.Marshal(thunderbirdBridgeResponse{ID: req.ID, OK: true, Data: json.RawMessage(`{"total":3}`)})
	if status, body := c.signed(http.MethodPost, "/v2/result", result); status != http.StatusNoContent {
		t.Fatalf("result status %d %s", status, body)
	}
	if err := <-done; err != nil || string(data) != `{"total":3}` {
		t.Fatalf("call: %s %v", data, err)
	}
	st := b.status(nil)
	if !st.Paired || st.State == "unpaired" || st.ExtensionVersion != "test" {
		t.Fatalf("status after pairing: %+v", st)
	}
	// The secret is persisted protected and survives a restart.
	_, stored, err := thunderbirdStoreFor(b.dataDir).load()
	if err != nil || stored == nil || !bytes.Equal(stored.Key, c.key) || stored.Origin != tbTestOrigin {
		t.Fatalf("persisted pairing: %+v %v", stored, err)
	}
}

func TestThunderbirdBridgeRejectsUnpairedAndWrongSecretClients(t *testing.T) {
	b, server := newTBTestServer(t)
	_ = pairedTBClient(t, b, server) // the real, paired extension
	b.queue <- thunderbirdBridgeRequest{ID: "secret-op", Op: "search"}
	b.waiters["secret-op"] = make(chan thunderbirdBridgeResponse, 1)

	// No pairing at all: knows the host id from hello, nothing else.
	stranger := &tbTestClient{t: t, base: server.URL, origin: tbTestOrigin}
	stranger.hostID = stranger.hello().HostID
	stranger.pairingID = "guess"
	stranger.key = bytes.Repeat([]byte{1}, 32)
	if got := stranger.auth(); got != "pairing_revoked" {
		t.Fatalf("unpaired client auth = %q", got)
	}
	// Right pairing id (it is not secret) but a wrong secret.
	b.mu.Lock()
	stranger.pairingID = b.auth.pairing.ID
	b.mu.Unlock()
	if got := stranger.auth(); got != "bad_proof" {
		t.Fatalf("wrong secret auth = %q", got)
	}
	// Signed requests without a valid session never reach the queue.
	stranger.session, stranger.sessKey = "forged-session", bytes.Repeat([]byte{2}, 32)
	if status, _ := stranger.signed(http.MethodGet, "/v2/poll", nil); status != http.StatusUnauthorized {
		t.Fatalf("forged session poll = %d", status)
	}
	resp, _ := stranger.raw(http.MethodGet, "/v2/poll", nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unsigned poll = %d", resp.StatusCode)
	}
	if len(b.queue) != 1 {
		t.Fatal("an unauthenticated client received a queued command")
	}
	if st := b.status(nil); st.LastRejected == nil || st.LastRejectedReason == "" {
		t.Fatalf("rejection not reported: %+v", st)
	}
}

func TestThunderbirdBridgePairingCodeRules(t *testing.T) {
	b, server := newTBTestServer(t)
	c := &tbTestClient{t: t, base: server.URL, origin: tbTestOrigin}
	// No active window: refused, even with a well-formed code.
	if err := c.pair("ABCD-EFGH-JKMN-PQRS"); err == nil || err.Error() != "pairing_not_active" {
		t.Fatalf("pairing without a window: %v", err)
	}
	code, err := b.startPairing()
	if err != nil {
		t.Fatal(err)
	}
	if hello := c.hello(); hello.Paired || hello.Challenge != "" {
		t.Fatal("unpaired host must not issue challenges")
	}
	for i := 0; i < thunderbirdPairingAttempts; i++ {
		if err := c.pair("0000-0000-0000-0000"); err == nil || err.Error() != "pairing_code_invalid" {
			t.Fatalf("wrong code attempt %d: %v", i, err)
		}
	}
	// Too many wrong codes close the window; the right code no longer works.
	if err := c.pair(code.Code); err == nil || err.Error() != "pairing_not_active" {
		t.Fatalf("window survived brute force: %v", err)
	}
	if st := b.status(nil); st.Paired || st.PairingActive {
		t.Fatalf("status after closed window: %+v", st)
	}
}

func TestThunderbirdBridgeTransportGuard(t *testing.T) {
	b, server := newTBTestServer(t)
	c := pairedTBClient(t, b, server)
	for _, origin := range []string{"https://evil.example", "http://127.0.0.1:8080", "null", "file://", "chrome-extension://abc"} {
		web := &tbTestClient{t: t, base: server.URL, origin: origin}
		for _, path := range []string{"/v2/hello", "/v2/poll", "/poll?token=supercli-thunderbird-local-v1-7b2e44c91a"} {
			resp, _ := web.raw(http.MethodGet, path, nil, nil)
			if resp.StatusCode != http.StatusForbidden || resp.Header.Get("Access-Control-Allow-Origin") != "" {
				t.Fatalf("origin %q %s: status=%d acao=%q", origin, path, resp.StatusCode, resp.Header.Get("Access-Control-Allow-Origin"))
			}
		}
		resp, _ := web.raw(http.MethodOptions, "/v2/poll", nil, http.Header{"Access-Control-Request-Method": {"GET"}})
		if resp.Header.Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("preflight from %q allowed", origin)
		}
	}
	// DNS rebinding: a request naming another host is refused.
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/v2/hello", nil)
	req.Host = "attacker.example:47831"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("wrong Host accepted: %d", resp.StatusCode)
	}
	// CORS is the exact paired origin, never "*".
	resp, _ = c.raw(http.MethodOptions, "/v2/poll", nil, nil)
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != tbTestOrigin {
		t.Fatalf("paired origin ACAO = %q", got)
	}
	other := &tbTestClient{t: t, base: server.URL, origin: "moz-extension://another-profile"}
	resp, _ = other.raw(http.MethodOptions, "/v2/poll", nil, nil)
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("another extension origin got ACAO %q on an authenticated endpoint", got)
	}
	// A different extension origin cannot use the paired session either.
	other.session, other.sessKey = c.session, c.sessKey
	if status, _ := other.signed(http.MethodGet, "/v2/poll", nil); status != http.StatusUnauthorized {
		t.Fatalf("session used from another origin: %d", status)
	}
}

func TestThunderbirdBridgeReplayTamperAndStaleRejected(t *testing.T) {
	b, server := newTBTestServer(t)
	c := pairedTBClient(t, b, server)
	b.waiters["r1"] = make(chan thunderbirdBridgeResponse, 1)
	body := []byte(`{"id":"r1","ok":true,"data":{}}`)
	s := c.sign(http.MethodPost, "/v2/result", body)
	if resp, data := c.raw(http.MethodPost, "/v2/result", body, s.header); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("first result %d %s", resp.StatusCode, data)
	}
	// Exact replay of the signed request (same nonce).
	resp, data := c.raw(http.MethodPost, "/v2/result", body, s.header)
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(string(data), "replay") {
		t.Fatalf("replay accepted: %d %s", resp.StatusCode, data)
	}
	// Same headers, different body.
	s2 := c.sign(http.MethodPost, "/v2/result", body)
	resp, data = c.raw(http.MethodPost, "/v2/result", []byte(`{"id":"r1","ok":false}`), s2.header)
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(string(data), "bad_signature") {
		t.Fatalf("tampered body accepted: %d %s", resp.StatusCode, data)
	}
	// Signed for another path.
	s3 := c.sign(http.MethodGet, "/v2/poll", nil)
	resp, _ = c.raw(http.MethodGet, "/v2/message-file?id=x", nil, s3.header)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("path substitution accepted: %d", resp.StatusCode)
	}
	// Stale timestamp with a valid MAC.
	old := strconv.FormatInt(time.Now().Add(-2*time.Minute).UnixMilli(), 10)
	nonce := thunderbirdRandomID(16)
	h := http.Header{}
	h.Set(headerSession, c.session)
	h.Set(headerTS, old)
	h.Set(headerNonce, nonce)
	h.Set(headerMAC, thunderbirdMAC64(c.sessKey, thunderbirdRequestMACFields("GET", "/v2/poll", old, nonce, thunderbirdBodyHash(nil))...))
	resp, data = c.raw(http.MethodGet, "/v2/poll", nil, h)
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(string(data), "stale") {
		t.Fatalf("stale request accepted: %d %s", resp.StatusCode, data)
	}
	// A captured challenge cannot be used twice.
	info := c.hello()
	for i, want := range []int{http.StatusOK, http.StatusUnauthorized} {
		extNonce := thunderbirdRandomID(32)
		resp, _ := c.postJSON("/v2/auth", map[string]any{
			"hostId": c.hostID, "pairingId": c.pairingID, "hostNonce": info.Challenge, "extNonce": extNonce,
			"mac": thunderbirdMAC64(c.key, "auth-ext", c.hostID, c.pairingID, info.Challenge, extNonce),
		})
		if resp.StatusCode != want {
			t.Fatalf("challenge use %d: status %d", i, resp.StatusCode)
		}
	}
}

func TestThunderbirdBridgeUnpairRevokesAndRepairReplaces(t *testing.T) {
	b, server := newTBTestServer(t)
	c := pairedTBClient(t, b, server)
	if err := b.unpair(); err != nil {
		t.Fatal(err)
	}
	if status, _ := c.signed(http.MethodGet, "/v2/poll", nil); status != http.StatusUnauthorized {
		t.Fatalf("session survived unpair: %d", status)
	}
	if got := c.auth(); got != "not_paired" {
		t.Fatalf("auth after unpair = %q", got)
	}
	if _, p, _ := thunderbirdStoreFor(b.dataDir).load(); p != nil {
		t.Fatal("unpair left the secret on disk")
	}
	// Re-pair: a new secret; the old one is refused.
	fresh := pairedTBClient(t, b, server)
	if bytes.Equal(fresh.key, c.key) {
		t.Fatal("re-pairing reused the old secret")
	}
	if got := c.auth(); got != "pairing_revoked" {
		t.Fatalf("old pairing after re-pair = %q", got)
	}
}

func TestThunderbirdBridgeOutdatedExtensionFailsClosed(t *testing.T) {
	b, server := newTBTestServer(t)
	_ = pairedTBClient(t, b, server)
	b.queue <- thunderbirdBridgeRequest{ID: "queued", Op: "search"}
	old := &tbTestClient{t: t, base: server.URL, origin: tbTestOrigin}
	for _, path := range []string{"/poll?token=supercli-thunderbird-local-v1-7b2e44c91a", "/result?token=supercli-thunderbird-local-v1-7b2e44c91a"} {
		resp, data := old.raw(http.MethodGet, path, nil, nil)
		if resp.StatusCode != http.StatusUpgradeRequired || !strings.Contains(string(data), "0.10.0") {
			t.Fatalf("%s: %d %s", path, resp.StatusCode, data)
		}
	}
	if len(b.queue) != 1 {
		t.Fatal("outdated extension received a command")
	}
	if err := b.notConnectedError(); !strings.Contains(err.Error(), "outdated") {
		t.Fatalf("not-connected message: %v", err)
	}
	if st := b.status(nil); !st.LegacyExtension {
		t.Fatal("status does not report the outdated extension")
	}
}

func TestThunderbirdBridgeUnpairedHostNeverListens(t *testing.T) {
	b := &thunderbirdBridgeState{
		queue:   make(chan thunderbirdBridgeRequest, 1),
		waiters: make(map[string]chan thunderbirdBridgeResponse),
		dataDir: t.TempDir(),
		addr:    freeLoopbackAddr(t),
	}
	start := time.Now()
	_, err := b.call(context.Background(), "status", json.RawMessage(`{}`))
	if !errors.Is(err, errThunderbirdNotPaired) || time.Since(start) > time.Second {
		t.Fatalf("unpaired call: %v after %s", err, time.Since(start))
	}
	if b.listener != nil {
		t.Fatal("unpaired host opened the bridge port")
	}
	if st := b.status(nil); st.State != "unpaired" || st.Listening {
		t.Fatalf("status %+v", st)
	}
}

func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// A second instance must not take over the port or be trusted; it reports
// who holds the port instead.
func TestThunderbirdBridgeSecondInstanceDoesNotTakeOver(t *testing.T) {
	addr := freeLoopbackAddr(t)
	first := &thunderbirdBridgeState{queue: make(chan thunderbirdBridgeRequest, 1), waiters: map[string]chan thunderbirdBridgeResponse{}, dataDir: t.TempDir(), addr: addr}
	second := &thunderbirdBridgeState{queue: make(chan thunderbirdBridgeRequest, 1), waiters: map[string]chan thunderbirdBridgeResponse{}, dataDir: t.TempDir(), addr: addr}
	newThunderbirdTestPeer(t, first)
	newThunderbirdTestPeer(t, second)
	if err := first.ensureServer(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		first.mu.Lock()
		first.stopServerLocked()
		first.mu.Unlock()
	})
	err := second.ensureServer()
	var busy *thunderbirdPortBusyError
	if !errors.As(err, &busy) || !strings.Contains(err.Error(), "another SuperCli instance") || !strings.Contains(err.Error(), "pid ") {
		t.Fatalf("second instance: %v", err)
	}
	if _, err := second.call(context.Background(), "count", json.RawMessage(`{}`)); !errors.As(err, &busy) {
		t.Fatalf("second instance call: %v", err)
	}
	if len(second.queue) != 0 {
		t.Fatal("second instance queued a command")
	}
	if st := second.status(err); st.State != "port_busy" || st.Listening {
		t.Fatalf("second status: %+v", st)
	}
	// Once the first instance frees the port the second one can take it.
	first.mu.Lock()
	first.stopServerLocked()
	first.mu.Unlock()
	second.mu.Lock()
	second.lastListenTry = time.Time{}
	second.mu.Unlock()
	if err := second.ensureServer(); err != nil {
		t.Fatalf("second instance after the port was freed: %v", err)
	}
	second.mu.Lock()
	second.stopServerLocked()
	second.mu.Unlock()
}

// Attachment uploads are accepted only with a valid signature over the body;
// a rejected upload leaves no file behind.
func TestThunderbirdBridgeSignedAttachmentUpload(t *testing.T) {
	b, server := newTBTestServer(t)
	c := pairedTBClient(t, b, server)
	path := "/v2/attachment-file?id=a1&filename=x.txt&content_type=text%2Fplain"
	body := []byte("attachment bytes")
	if status, data := c.signed(http.MethodPost, path, body); status != http.StatusOK {
		t.Fatalf("upload %d %s", status, data)
	}
	if _, ok := b.downloadedAttachment("a1"); !ok {
		t.Fatal("signed upload not registered")
	}
	s := c.sign(http.MethodPost, strings.Replace(path, "a1", "a2", 1), body)
	resp, _ := c.raw(http.MethodPost, strings.Replace(path, "a1", "a2", 1), []byte("other bytes"), s.header)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("tampered upload accepted: %d", resp.StatusCode)
	}
	if _, ok := b.downloadedAttachment("a2"); ok {
		t.Fatal("tampered upload registered")
	}
}

// Floods from web pages or local processes must not lock the paired
// extension out: rejected origins are not rate-limited failures and a hello
// flood only evicts the flooder's own challenges.
func TestThunderbirdBridgeFloodsDoNotLockOutThePairedExtension(t *testing.T) {
	b, server := newTBTestServer(t)
	c := pairedTBClient(t, b, server)
	web := &tbTestClient{t: t, base: server.URL, origin: "https://evil.example"}
	local := &tbTestClient{t: t, base: server.URL}
	for i := 0; i < 2*thunderbirdFailureLimit; i++ {
		web.raw(http.MethodGet, "/v2/hello", nil, nil)
		local.hello()
		local.postJSON("/v2/auth", map[string]any{"hostId": c.hostID, "pairingId": c.pairingID, "hostNonce": "x", "extNonce": "y", "mac": "z"})
	}
	if reason := c.auth(); reason != "" {
		t.Fatalf("paired extension locked out after floods: %s", reason)
	}
}
