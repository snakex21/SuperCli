package mail

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// thunderbirdTestPeer stands in for a paired, authenticated extension: it
// installs an in-memory pairing and session in the bridge state (never on
// disk) and signs requests exactly as extension/bridge-auth.js does.
type thunderbirdTestPeer struct {
	b          *thunderbirdBridgeState
	sessionID  string
	sessionKey []byte
	lastNonce  string
}

func newThunderbirdTestPeer(t *testing.T, b *thunderbirdBridgeState) *thunderbirdTestPeer {
	if t != nil {
		t.Helper()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	a := &b.auth
	if !a.loaded {
		a.loaded = true
		a.hostID = "test-host"
		a.pairing = &thunderbirdPairing{ID: "test-pairing", Key: bytes.Repeat([]byte{7}, 32), PairedAt: time.Now()}
	}
	if a.sessions == nil {
		a.sessions = make(map[string]*thunderbirdSession)
	}
	if a.challenges == nil {
		a.challenges = make(map[string]time.Time)
	}
	id := thunderbirdRandomID(18)
	key := thunderbirdSessionKey(a.pairing.Key, "h", "e", id)
	a.sessions[id] = &thunderbirdSession{id: id, key: key, created: time.Now(), lastSeen: time.Now(), nonces: map[string]time.Time{}}
	return &thunderbirdTestPeer{b: b, sessionID: id, sessionKey: key}
}

func (p *thunderbirdTestPeer) sign(req *http.Request, bodyHash string) *http.Request {
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	nonce := thunderbirdRandomID(16)
	p.lastNonce = nonce
	req.Host = p.b.listenAddr()
	req.Header.Set(headerSession, p.sessionID)
	req.Header.Set(headerTS, ts)
	req.Header.Set(headerNonce, nonce)
	req.Header.Set(headerMAC, thunderbirdMAC64(p.sessionKey, thunderbirdRequestMACFields(req.Method, req.URL.RequestURI(), ts, nonce, bodyHash)...))
	return req
}

// request builds a signed request for a handler called directly.
func (p *thunderbirdTestPeer) request(method, target string, body []byte) *http.Request {
	return p.sign(httptest.NewRequest(method, target, bytes.NewReader(body)), thunderbirdBodyHash(body))
}

// requestStream signs a body that cannot be hashed up front; the handler
// must fail before the MAC check (used for broken upload streams).
func (p *thunderbirdTestPeer) requestStream(method, target string, body io.Reader) *http.Request {
	return p.sign(httptest.NewRequest(method, target, body), thunderbirdBodyHash(nil))
}

// verify checks the host's response signature for the last request.
func (p *thunderbirdTestPeer) verify(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	ts := rec.Header().Get(headerTS)
	fields := thunderbirdResponseMACFields(rec.Code, p.lastNonce, ts, thunderbirdBodyHash(rec.Body.Bytes()))
	if !thunderbirdVerifyMAC(p.sessionKey, rec.Header().Get(headerMAC), fields...) {
		t.Fatalf("response signature invalid: status=%d", rec.Code)
	}
}
