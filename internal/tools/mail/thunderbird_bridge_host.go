package mail

import (
	"bytes"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"supercli/internal/buildinfo"
)

// Protocol 2 transport of the Thunderbird Bridge host: pairing, mutual
// authentication, per-message signatures and the strict HTTP envelope
// (exact Host, no web origins, CORS only for the paired extension).

const (
	thunderbirdPairingWindow   = 5 * time.Minute
	thunderbirdPairingAttempts = 5
	thunderbirdChallengeTTL    = 30 * time.Second
	thunderbirdChallengeLimit  = 64
	thunderbirdSessionIdle     = 10 * time.Minute
	thunderbirdSessionLimit    = 4
	thunderbirdFailureWindow   = time.Minute
	thunderbirdFailureLimit    = 30
	thunderbirdMaxJSONBody     = 64 << 10
	thunderbirdMaxResultBody   = 4 << 20

	headerSession = "X-TB-Session"
	headerTS      = "X-TB-Ts"
	headerNonce   = "X-TB-Nonce"
	headerMAC     = "X-TB-Mac"
)

type thunderbirdSession struct {
	id         string
	key        []byte
	origin     string
	created    time.Time
	lastSeen   time.Time
	extVersion string
	nonces     map[string]time.Time
}

type thunderbirdPairingWindowState struct {
	codeKey   []byte
	expires   time.Time
	attempts  int
	pending   *thunderbirdPendingPairing
	startedAt time.Time
}

type thunderbirdPendingPairing struct {
	key        []byte
	transcript string
	origin     string
	extVersion string
}

// thunderbirdAuthState is guarded by thunderbirdBridgeState.mu.
type thunderbirdAuthState struct {
	loaded     bool
	loadErr    error
	hostID     string
	pairing    *thunderbirdPairing
	window     *thunderbirdPairingWindowState
	challenges map[string]time.Time
	sessions   map[string]*thunderbirdSession
	failures   []time.Time

	legacySeen     time.Time
	rejectedAt     time.Time
	rejectedReason string
	lastAuthAt     time.Time
}

// thunderbirdHostInfo is what /v2/hello tells any local client. It contains no
// secret: it lets the extension pick the matching pairing and lets a second
// SuperCli instance explain who holds the port.
type thunderbirdHostInfo struct {
	Protocol   int    `json:"proto"`
	HostID     string `json:"hostId"`
	App        string `json:"app"`
	AppVersion string `json:"appVersion"`
	PID        int    `json:"pid"`
	Paired     bool   `json:"paired"`
	Challenge  string `json:"challenge,omitempty"`
}

func thunderbirdAppName() string {
	if exe, err := os.Executable(); err == nil {
		return strings.TrimSuffix(filepath.Base(exe), filepath.Ext(exe))
	}
	return "supercli"
}

// --- state loading -------------------------------------------------------

// authLocked loads the host id and pairing once. Caller holds b.mu.
func (b *thunderbirdBridgeState) authLocked() *thunderbirdAuthState {
	a := &b.auth
	if a.challenges == nil {
		a.challenges = make(map[string]time.Time)
	}
	if a.sessions == nil {
		a.sessions = make(map[string]*thunderbirdSession)
	}
	if a.loaded {
		return a
	}
	if b.dataDir == "" {
		root, err := thunderbirdRuntimeDataDir("")
		if err != nil {
			a.loadErr = err
			return a
		}
		b.dataDir = root
	}
	hostID, pairing, err := thunderbirdStoreFor(b.dataDir).load()
	a.loaded = true
	a.hostID = hostID
	a.pairing = pairing
	a.loadErr = err
	if hostID == "" && err != nil {
		// No identity could be created (unwritable data folder): use an
		// ephemeral one so the host still answers, but it cannot be paired.
		a.hostID = thunderbirdRandomID(12)
	}
	return a
}

func (b *thunderbirdBridgeState) savePairingLocked(p *thunderbirdPairing) error {
	a := b.authLocked()
	if b.dataDir == "" {
		return errors.New("no data directory for the pairing")
	}
	if err := thunderbirdStoreFor(b.dataDir).save(a.hostID, p); err != nil {
		return err
	}
	a.pairing = p
	a.loadErr = nil
	// Sessions belong to the previous pairing.
	a.sessions = make(map[string]*thunderbirdSession)
	return nil
}

// noteRejectedLocked records why an extension was refused, for the status
// shown to the user. It does not rate-limit anything: authentication against a
// 256-bit secret needs no throttling, and counting such failures would let any
// web page or local process lock the real extension out.
func (b *thunderbirdBridgeState) noteRejectedLocked(reason string) {
	a := b.authLocked()
	a.rejectedAt = time.Now()
	a.rejectedReason = reason
}

// notePairingFailureLocked counts a failed pairing-code attempt towards the
// global limit (codes are short, so guessing must stay slow).
func (b *thunderbirdBridgeState) notePairingFailureLocked(reason string) {
	b.noteRejectedLocked(reason)
	a := b.authLocked()
	a.failures = append(a.failures, time.Now())
}

func (b *thunderbirdBridgeState) rateLimitedLocked() bool {
	a := b.authLocked()
	cutoff := time.Now().Add(-thunderbirdFailureWindow)
	kept := a.failures[:0]
	for _, at := range a.failures {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	a.failures = kept
	return len(a.failures) >= thunderbirdFailureLimit
}

// --- HTTP envelope ---------------------------------------------------------

func thunderbirdIsExtensionOrigin(origin string) bool {
	return strings.HasPrefix(origin, "moz-extension://") && len(origin) > len("moz-extension://")
}

// handler builds the bridge mux behind the transport guard.
func (b *thunderbirdBridgeState) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/hello", b.handleHello)
	mux.HandleFunc("/v2/pair/start", b.handlePairStart)
	mux.HandleFunc("/v2/pair/finish", b.handlePairFinish)
	mux.HandleFunc("/v2/auth", b.handleAuth)
	mux.HandleFunc("/v2/poll", b.handlePoll)
	mux.HandleFunc("/v2/result", b.handleResult)
	mux.HandleFunc("/v2/message-file", b.handleMessageFile)
	mux.HandleFunc("/v2/attachment-file", b.handleAttachmentFile)
	for _, legacy := range []string{"/poll", "/result", "/health", "/message-file", "/attachment-file"} {
		mux.HandleFunc(legacy, b.handleLegacy)
	}
	return b.guard(mux)
}

// guard enforces the transport rules before any handler runs:
//   - the Host header must name the bridge address exactly (DNS rebinding);
//   - a request carrying a web Origin (http, https, file, null, another
//     browser's extension) is refused outright;
//   - CORS headers are only ever echoed for a moz-extension origin, never "*".
func (b *thunderbirdBridgeState) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Vary", "Origin")
		if r.Host != b.listenAddr() {
			http.Error(w, "wrong host", http.StatusMisdirectedRequest)
			return
		}
		origin := r.Header.Get("Origin")
		if origin != "" && !thunderbirdIsExtensionOrigin(origin) {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		if origin != "" && b.corsAllowed(r.URL.Path, origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, "+headerSession+", "+headerTS+", "+headerNonce+", "+headerMAC)
			w.Header().Set("Access-Control-Expose-Headers", headerTS+", "+headerMAC)
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// corsAllowed lets any Thunderbird extension read the unauthenticated
// handshake answers (so an unpaired or outdated client gets a readable
// reason), but only the paired extension's exact origin may read the
// authenticated endpoints.
func (b *thunderbirdBridgeState) corsAllowed(path, origin string) bool {
	switch path {
	case "/v2/hello", "/v2/pair/start", "/v2/pair/finish", "/v2/auth":
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	a := b.authLocked()
	return a.pairing != nil && a.pairing.Origin != "" && a.pairing.Origin == origin
}

func (b *thunderbirdBridgeState) listenAddr() string {
	if b.addr != "" {
		return b.addr
	}
	return thunderbirdBridgeAddr
}

func writeThunderbirdJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func thunderbirdAuthError(w http.ResponseWriter, status int, code, message string) {
	writeThunderbirdJSON(w, status, map[string]any{"ok": false, "error": code, "message": message})
}

// handleLegacy answers the protocol 1 endpoints (fixed token in the URL) of an
// outdated extension. It never dispatches anything: the old token is public.
func (b *thunderbirdBridgeState) handleLegacy(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	b.authLocked().legacySeen = time.Now()
	b.mu.Unlock()
	thunderbirdAuthError(w, http.StatusUpgradeRequired, "extension_outdated",
		"This SuperCli requires Thunderbird AI Bridge 0.10.0 or newer with pairing. Update the extension and pair it.")
}

// --- hello and pairing -----------------------------------------------------

func (b *thunderbirdBridgeState) handleHello(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	b.mu.Lock()
	a := b.authLocked()
	now := time.Now()
	for nonce, expires := range a.challenges {
		if now.After(expires) {
			delete(a.challenges, nonce)
		}
	}
	info := thunderbirdHostInfo{
		Protocol:   thunderbirdProtocolVersion,
		HostID:     a.hostID,
		App:        thunderbirdAppName(),
		AppVersion: buildinfo.Version,
		PID:        os.Getpid(),
		Paired:     a.pairing != nil,
	}
	if a.pairing != nil {
		// Never refuse a challenge: a flood of hello requests from another
		// local process evicts its own oldest challenges, not the extension.
		for len(a.challenges) >= thunderbirdChallengeLimit {
			var oldest string
			var at time.Time
			for nonce, expires := range a.challenges {
				if oldest == "" || expires.Before(at) {
					oldest, at = nonce, expires
				}
			}
			delete(a.challenges, oldest)
		}
		info.Challenge = thunderbirdRandomID(32)
		a.challenges[info.Challenge] = now.Add(thunderbirdChallengeTTL)
	}
	b.mu.Unlock()
	writeThunderbirdJSON(w, http.StatusOK, info)
}

func decodeThunderbirdJSON(w http.ResponseWriter, r *http.Request, limit int64, value any) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	defer r.Body.Close()
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(value); err != nil {
		thunderbirdAuthError(w, http.StatusBadRequest, "bad_request", "malformed JSON")
		return false
	}
	return true
}

type thunderbirdPairStartRequest struct {
	Protocol   int    `json:"proto"`
	ExtPub     string `json:"extPub"`
	ExtNonce   string `json:"extNonce"`
	ExtVersion string `json:"extVersion"`
	MAC        string `json:"mac"`
}

func (b *thunderbirdBridgeState) handlePairStart(w http.ResponseWriter, r *http.Request) {
	var req thunderbirdPairStartRequest
	if !decodeThunderbirdJSON(w, r, thunderbirdMaxJSONBody, &req) {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	a := b.authLocked()
	if b.rateLimitedLocked() {
		thunderbirdAuthError(w, http.StatusTooManyRequests, "rate_limited", "too many failed attempts; wait a minute")
		return
	}
	win := a.window
	if win == nil || time.Now().After(win.expires) {
		a.window = nil
		b.noteRejectedLocked("pairing attempt while no pairing code was active")
		thunderbirdAuthError(w, http.StatusForbidden, "pairing_not_active",
			"No pairing code is active in SuperCli. Start pairing there first, then enter the new code.")
		return
	}
	if req.Protocol != thunderbirdProtocolVersion || !thunderbirdValidNonce(req.ExtNonce) {
		thunderbirdAuthError(w, http.StatusBadRequest, "bad_request", "unsupported pairing request")
		return
	}
	if !thunderbirdVerifyMAC(win.codeKey, req.MAC, "pair-start", req.ExtPub, req.ExtNonce) {
		win.attempts++
		b.notePairingFailureLocked("wrong pairing code")
		if win.attempts >= thunderbirdPairingAttempts {
			a.window = nil
			thunderbirdAuthError(w, http.StatusForbidden, "pairing_code_invalid",
				"Wrong pairing code. Too many attempts: start pairing again in SuperCli.")
			return
		}
		thunderbirdAuthError(w, http.StatusForbidden, "pairing_code_invalid", "Wrong pairing code.")
		return
	}
	extPubRaw, err := b64.DecodeString(req.ExtPub)
	if err != nil {
		thunderbirdAuthError(w, http.StatusBadRequest, "bad_request", "invalid public key")
		return
	}
	curve := ecdh.P256()
	extPub, err := curve.NewPublicKey(extPubRaw)
	if err != nil {
		thunderbirdAuthError(w, http.StatusBadRequest, "bad_request", "invalid public key")
		return
	}
	hostPriv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		thunderbirdAuthError(w, http.StatusInternalServerError, "internal", "key generation failed")
		return
	}
	shared, err := hostPriv.ECDH(extPub)
	if err != nil {
		thunderbirdAuthError(w, http.StatusBadRequest, "bad_request", "key agreement failed")
		return
	}
	hostPub := b64.EncodeToString(hostPriv.PublicKey().Bytes())
	hostNonce := thunderbirdRandomID(32)
	transcript := thunderbirdPairTranscript(a.hostID, req.ExtPub, hostPub, req.ExtNonce, hostNonce)
	key, err := thunderbirdPairingKey(shared, win.codeKey, transcript)
	if err != nil {
		thunderbirdAuthError(w, http.StatusInternalServerError, "internal", "key derivation failed")
		return
	}
	win.pending = &thunderbirdPendingPairing{
		key:        key,
		transcript: transcript,
		origin:     r.Header.Get("Origin"),
		extVersion: thunderbirdCleanLabel(req.ExtVersion),
	}
	writeThunderbirdJSON(w, http.StatusOK, map[string]any{
		"proto":      thunderbirdProtocolVersion,
		"hostId":     a.hostID,
		"hostPub":    hostPub,
		"hostNonce":  hostNonce,
		"app":        thunderbirdAppName(),
		"appVersion": buildinfo.Version,
		"mac":        thunderbirdMAC64(key, "pair-host", transcript),
	})
}

// thunderbirdPairTranscript is the hash of everything both sides contributed.
func thunderbirdPairTranscript(hostID, extPub, hostPub, extNonce, hostNonce string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{thunderbirdProtocolName + " pair", hostID, extPub, hostPub, extNonce, hostNonce}, "\n")))
	return b64.EncodeToString(sum[:])
}

// thunderbirdPairingKey derives the 256-bit pairing secret from the ECDH
// shared value, salted with the code key: an observer of the pairing traffic
// who later learns the code still cannot compute it.
func thunderbirdPairingKey(shared, codeKey []byte, transcript string) ([]byte, error) {
	return hkdf.Key(sha256.New, shared, codeKey, thunderbirdProtocolName+" pairing key\n"+transcript, 32)
}

func thunderbirdCleanLabel(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 40 {
		value = value[:40]
	}
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, value)
}

type thunderbirdPairFinishRequest struct {
	HostID string `json:"hostId"`
	MAC    string `json:"mac"`
}

func (b *thunderbirdBridgeState) handlePairFinish(w http.ResponseWriter, r *http.Request) {
	var req thunderbirdPairFinishRequest
	if !decodeThunderbirdJSON(w, r, thunderbirdMaxJSONBody, &req) {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	a := b.authLocked()
	win := a.window
	if win == nil || win.pending == nil || time.Now().After(win.expires) {
		thunderbirdAuthError(w, http.StatusForbidden, "pairing_not_active", "Pairing expired. Start it again in SuperCli.")
		return
	}
	pending := win.pending
	if req.HostID != a.hostID || pending.origin != r.Header.Get("Origin") ||
		!thunderbirdVerifyMAC(pending.key, req.MAC, "pair-ext", pending.transcript) {
		win.pending = nil
		win.attempts++
		b.notePairingFailureLocked("pairing confirmation failed")
		thunderbirdAuthError(w, http.StatusForbidden, "pairing_failed", "Pairing confirmation failed.")
		return
	}
	pairing := &thunderbirdPairing{
		ID:               thunderbirdRandomID(12),
		Key:              pending.key,
		Origin:           pending.origin,
		PairedAt:         time.Now().UTC(),
		ExtensionVersion: pending.extVersion,
	}
	if err := b.savePairingLocked(pairing); err != nil {
		thunderbirdAuthError(w, http.StatusInternalServerError, "store_failed", "SuperCli could not save the pairing.")
		return
	}
	a.window = nil
	writeThunderbirdJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"pairingId": pairing.ID,
		"mac":       thunderbirdMAC64(pairing.Key, "pair-done", pending.transcript, pairing.ID),
	})
}

// --- mutual authentication -------------------------------------------------

type thunderbirdAuthRequest struct {
	HostID     string `json:"hostId"`
	PairingID  string `json:"pairingId"`
	HostNonce  string `json:"hostNonce"`
	ExtNonce   string `json:"extNonce"`
	ExtVersion string `json:"extVersion"`
	MAC        string `json:"mac"`
}

func (b *thunderbirdBridgeState) handleAuth(w http.ResponseWriter, r *http.Request) {
	var req thunderbirdAuthRequest
	if !decodeThunderbirdJSON(w, r, thunderbirdMaxJSONBody, &req) {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	a := b.authLocked()
	now := time.Now()
	expires, known := a.challenges[req.HostNonce]
	delete(a.challenges, req.HostNonce) // single use, even on failure
	switch {
	case a.pairing == nil:
		b.noteRejectedLocked("extension is not paired with this SuperCli")
		thunderbirdAuthError(w, http.StatusUnauthorized, "not_paired", "This SuperCli is not paired with any Thunderbird. Pair it again.")
		return
	case req.HostID != a.hostID:
		b.noteRejectedLocked("extension is paired with a different SuperCli")
		thunderbirdAuthError(w, http.StatusUnauthorized, "wrong_host", "This Thunderbird is paired with a different SuperCli.")
		return
	case req.PairingID != a.pairing.ID:
		b.noteRejectedLocked("extension uses an old or revoked pairing")
		thunderbirdAuthError(w, http.StatusUnauthorized, "pairing_revoked", "This pairing was replaced or removed in SuperCli. Pair again.")
		return
	case !known || now.After(expires) || !thunderbirdValidNonce(req.ExtNonce):
		b.noteRejectedLocked("expired or unknown challenge")
		thunderbirdAuthError(w, http.StatusUnauthorized, "challenge_invalid", "Challenge expired; retry.")
		return
	case a.pairing.Origin != "" && r.Header.Get("Origin") != "" && r.Header.Get("Origin") != a.pairing.Origin:
		b.noteRejectedLocked("request from a different extension origin than the paired one")
		thunderbirdAuthError(w, http.StatusUnauthorized, "origin_mismatch", "Request does not come from the paired Thunderbird profile.")
		return
	case !thunderbirdVerifyMAC(a.pairing.Key, req.MAC, "auth-ext", a.hostID, a.pairing.ID, req.HostNonce, req.ExtNonce):
		b.noteRejectedLocked("wrong pairing secret")
		thunderbirdAuthError(w, http.StatusUnauthorized, "bad_proof", "Authentication failed.")
		return
	}
	sessionID := thunderbirdRandomID(18)
	sess := &thunderbirdSession{
		id:         sessionID,
		key:        thunderbirdSessionKey(a.pairing.Key, req.HostNonce, req.ExtNonce, sessionID),
		origin:     r.Header.Get("Origin"),
		created:    now,
		lastSeen:   now,
		extVersion: thunderbirdCleanLabel(req.ExtVersion),
		nonces:     make(map[string]time.Time),
	}
	b.pruneSessionsLocked(now)
	for len(a.sessions) >= thunderbirdSessionLimit {
		var oldest *thunderbirdSession
		for _, s := range a.sessions {
			if oldest == nil || s.lastSeen.Before(oldest.lastSeen) {
				oldest = s
			}
		}
		delete(a.sessions, oldest.id)
	}
	a.sessions[sessionID] = sess
	a.lastAuthAt = now
	if sess.extVersion != "" && sess.extVersion != a.pairing.ExtensionVersion {
		a.pairing.ExtensionVersion = sess.extVersion
	}
	writeThunderbirdJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"sessionId":     sessionID,
		"idleTimeoutMs": thunderbirdSessionIdle.Milliseconds(),
		"mac":           thunderbirdMAC64(a.pairing.Key, "auth-host", a.hostID, a.pairing.ID, req.HostNonce, req.ExtNonce, sessionID),
	})
}

func (b *thunderbirdBridgeState) pruneSessionsLocked(now time.Time) {
	a := b.authLocked()
	for id, s := range a.sessions {
		if now.Sub(s.lastSeen) > thunderbirdSessionIdle {
			delete(a.sessions, id)
		}
	}
}

// --- signed requests and responses ----------------------------------------

type thunderbirdSigned struct {
	session *thunderbirdSession
	nonce   string
}

var errThunderbirdUnauthenticated = errors.New("unauthenticated")

// precheckSigned validates everything except the body MAC: session, origin,
// timestamp and nonce syntax. It runs before a body is read.
func (b *thunderbirdBridgeState) precheckSigned(r *http.Request) (*thunderbirdSession, string, string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	a := b.authLocked()
	now := time.Now()
	b.pruneSessionsLocked(now)
	sess := a.sessions[r.Header.Get(headerSession)]
	if sess == nil {
		return nil, "", "session_invalid"
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != sess.origin {
		return nil, "", "origin_mismatch"
	}
	if thunderbirdCheckTimestamp(r.Header.Get(headerTS), now.UnixMilli()) != nil {
		return nil, "", "stale"
	}
	nonce := r.Header.Get(headerNonce)
	if !thunderbirdValidNonce(nonce) {
		return nil, "", "bad_nonce"
	}
	if _, seen := sess.nonces[nonce]; seen {
		return nil, "", "replay"
	}
	return sess, nonce, ""
}

// finishSigned verifies the MAC over the body hash and records the nonce
// atomically, so two concurrent copies of one request cannot both pass.
func (b *thunderbirdBridgeState) finishSigned(r *http.Request, sess *thunderbirdSession, nonce, bodyHash string) string {
	fields := thunderbirdRequestMACFields(r.Method, r.URL.RequestURI(), r.Header.Get(headerTS), nonce, bodyHash)
	if !thunderbirdVerifyMAC(sess.key, r.Header.Get(headerMAC), fields...) {
		b.mu.Lock()
		b.noteRejectedLocked("bad request signature")
		b.mu.Unlock()
		return "bad_signature"
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.auth.sessions[sess.id] != sess {
		return "session_invalid"
	}
	if _, seen := sess.nonces[nonce]; seen {
		b.noteRejectedLocked("replayed request")
		return "replay"
	}
	now := time.Now()
	for n, expires := range sess.nonces {
		if now.After(expires) {
			delete(sess.nonces, n)
		}
	}
	// A nonce stays remembered for longer than a timestamp stays valid, so a
	// replay is caught either by the cache or by the clock check.
	sess.nonces[nonce] = now.Add(3 * thunderbirdMaxSkewMillis * time.Millisecond)
	sess.lastSeen = now
	return ""
}

// authenticate reads a bounded body and verifies a signed request. On failure
// it has already answered with 401.
func (b *thunderbirdBridgeState) authenticate(w http.ResponseWriter, r *http.Request, limit int64) (*thunderbirdSigned, []byte, bool) {
	sess, nonce, reason := b.precheckSigned(r)
	if reason != "" {
		thunderbirdAuthError(w, http.StatusUnauthorized, reason, "authenticate again")
		return nil, nil, false
	}
	var body []byte
	if r.Body != nil {
		defer r.Body.Close()
		var err error
		body, err = io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
		if err != nil {
			thunderbirdAuthError(w, http.StatusRequestEntityTooLarge, "too_large", "request body too large")
			return nil, nil, false
		}
	}
	if reason := b.finishSigned(r, sess, nonce, thunderbirdBodyHash(body)); reason != "" {
		thunderbirdAuthError(w, http.StatusUnauthorized, reason, "authenticate again")
		return nil, nil, false
	}
	return &thunderbirdSigned{session: sess, nonce: nonce}, body, true
}

// writeSigned answers with a MAC binding status, request nonce and body.
func (s *thunderbirdSigned) write(w http.ResponseWriter, status int, contentType string, body []byte) {
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	w.Header().Set(headerTS, ts)
	w.Header().Set(headerMAC, thunderbirdMAC64(s.session.key, thunderbirdResponseMACFields(status, s.nonce, ts, thunderbirdBodyHash(body))...))
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if len(body) > 0 {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	}
	w.WriteHeader(status)
	if len(body) > 0 {
		_, _ = w.Write(body)
	}
}

func (s *thunderbirdSigned) writeJSON(w http.ResponseWriter, status int, value any) {
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(value)
	s.write(w, status, "application/json", buf.Bytes())
}

func (s *thunderbirdSigned) writeError(w http.ResponseWriter, status int, message string) {
	s.writeJSON(w, status, map[string]any{"ok": false, "error": message})
}

// --- listener ---------------------------------------------------------------

// ensureServer listens on the bridge port when this host is paired or a
// pairing code is active. An unpaired instance never occupies the port, so a
// test or second copy cannot shadow the paired one. A failed listen is retried
// on the next call (the other instance may have exited).
func (b *thunderbirdBridgeState) ensureServer() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.testNoListen {
		return nil
	}
	if b.listener != nil {
		return nil
	}
	a := b.authLocked()
	if a.pairing == nil && a.window == nil {
		if a.loadErr != nil {
			return a.loadErr
		}
		return errThunderbirdNotPaired
	}
	if !b.lastListenTry.IsZero() && time.Since(b.lastListenTry) < 2*time.Second && b.startErr != nil {
		return b.startErr
	}
	b.lastListenTry = time.Now()
	addr := b.listenAddr()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		b.startErr = &thunderbirdPortBusyError{addr: addr, cause: err}
		return b.startErr
	}
	b.startErr = nil
	b.listener = ln
	server := &http.Server{Handler: b.handler(), ReadHeaderTimeout: 5 * time.Second}
	b.server = server
	go func() { _ = server.Serve(ln) }()
	return nil
}

func (b *thunderbirdBridgeState) stopServerLocked() {
	if b.server != nil {
		_ = b.server.Close()
	}
	b.server = nil
	b.listener = nil
}

var errThunderbirdNotPaired = errors.New("Thunderbird Bridge is not paired with this SuperCli. Pair it once: SuperCli GUI Settings -> Thunderbird -> Pair (TUI: /thunderbird pair; NestCafe: Settings -> Integrations -> Thunderbird), then enter the code in Thunderbird -> Add-ons -> Thunderbird AI Bridge -> Options. Nothing was sent to Thunderbird")

type thunderbirdPortBusyError struct {
	addr  string
	cause error
}

func (e *thunderbirdPortBusyError) Error() string {
	owner := describeThunderbirdPortOwner(e.addr)
	return fmt.Sprintf("Thunderbird Bridge port %s is used by %s; this instance did not take it over and sent nothing to Thunderbird. Close the other instance (or use it) and try again", e.addr, owner)
}

func (e *thunderbirdPortBusyError) Unwrap() error { return e.cause }

// probeThunderbirdPortOwner asks whoever holds the port who it is. It sends no
// secret and trusts the answer only for display.
func probeThunderbirdPortOwner(addr string) (*thunderbirdHostInfo, int) {
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get("http://" + addr + "/v2/hello")
	if err != nil {
		return nil, 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode
	}
	var info thunderbirdHostInfo
	if json.NewDecoder(io.LimitReader(resp.Body, 8<<10)).Decode(&info) != nil || info.Protocol == 0 {
		return nil, resp.StatusCode
	}
	return &info, resp.StatusCode
}

func describeThunderbirdPortOwner(addr string) string {
	info, status := probeThunderbirdPortOwner(addr)
	switch {
	case info != nil:
		return fmt.Sprintf("another SuperCli instance (%s %s, pid %d)", thunderbirdCleanLabel(info.App), thunderbirdCleanLabel(info.AppVersion), info.PID)
	case status == http.StatusNotFound:
		return "an older SuperCli/NestCafe without pairing (1.0.24 or earlier)"
	default:
		return "another process"
	}
}
