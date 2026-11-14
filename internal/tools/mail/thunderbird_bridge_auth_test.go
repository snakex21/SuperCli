package mail

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These vectors are computed by extension/bridge-auth.js (WebCrypto) in the
// Thunderbird-AI-Bridge repository (tests/bridge-auth.test.js asserts the
// same values). A mismatch means the two implementations no longer agree.
func TestThunderbirdBridgeVectorsMatchExtension(t *testing.T) {
	normalized, ok := thunderbirdNormalizePairingCode("abcd-efgh-jkmn-pqrs")
	if !ok || normalized != "ABCDEFGHJKMNPQRS" {
		t.Fatalf("normalize: %q %v", normalized, ok)
	}
	codeKey := thunderbirdCodeKey(normalized)
	if got := b64.EncodeToString(codeKey); got != "PfYzD32joV33Fl8OIZEDLzM3EaZbOBfjYeT3Y2xyySs" {
		t.Fatalf("code key %s", got)
	}
	transcript := thunderbirdPairTranscript("host-1", "extpub", "hostpub", "en", "hn")
	if transcript != "5KdZu0H-a9ZU60vbBS8jclaCyoFvTfeiaGn3MmLFUBg" {
		t.Fatalf("transcript %s", transcript)
	}
	shared := make([]byte, 32)
	for i := range shared {
		shared[i] = byte(i)
	}
	key, err := thunderbirdPairingKey(shared, codeKey, transcript)
	if err != nil || b64.EncodeToString(key) != "1dDkYKVuspynCvVtCaddDpACV7zehowBxemVJN4q6xI" {
		t.Fatalf("pairing key %s err=%v", b64.EncodeToString(key), err)
	}
	session := thunderbirdSessionKey(key, "hn2", "en2", "sid")
	if got := b64.EncodeToString(session); got != "7dUX8O2G2p3WjC3bMKCKFGyaZ8E4pzppVBCqd9JuOpE" {
		t.Fatalf("session key %s", got)
	}
	bodyHash := thunderbirdBodyHash([]byte("{}"))
	if bodyHash != "RBNvo1WzZ4oRRq0W9-hknpT7T8If536DEMBg9hyq_4o" {
		t.Fatalf("body hash %s", bodyHash)
	}
	mac := thunderbirdMAC64(session, thunderbirdRequestMACFields("post", "/v2/result", "1700000000000", "nonce-0123456789abcdef", bodyHash)...)
	if mac != "ZZmrabyx4O--5l2jfPDOf0Uzeu483y2ezIa-_f1QLtM" {
		t.Fatalf("request mac %s", mac)
	}
}

func TestThunderbirdPairingCodeShapeAndNormalization(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		code := thunderbirdNewPairingCode()
		if len(code) != thunderbirdCodeLength {
			t.Fatalf("code length %d", len(code))
		}
		for _, r := range code {
			if !strings.ContainsRune(thunderbirdCodeAlphabet, r) {
				t.Fatalf("code %q has %q outside the alphabet", code, r)
			}
		}
		if seen[code] {
			t.Fatal("pairing codes repeat")
		}
		seen[code] = true
		formatted := thunderbirdFormatPairingCode(code)
		if len(formatted) != 19 || strings.Count(formatted, "-") != 3 {
			t.Fatalf("formatted %q", formatted)
		}
		if back, ok := thunderbirdNormalizePairingCode(" " + strings.ToLower(formatted) + " "); !ok || back != code {
			t.Fatalf("round trip %q -> %q", formatted, back)
		}
	}
	if got, ok := thunderbirdNormalizePairingCode("oil0-OIL0-0000-0000"); !ok || got != "0110011000000000" {
		t.Fatalf("look-alikes: %q %v", got, ok)
	}
	for _, bad := range []string{"", "ABCD-EFGH-JKMN-PQR", "ABCD-EFGH-JKMN-PQRSX", "ABCD-EFGH-JKMN-PQRU", "ABCD EFGH JKMN PQR!"} {
		if _, ok := thunderbirdNormalizePairingCode(bad); ok {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestThunderbirdMACRejectsTamperingAndMalformedInput(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	fields := thunderbirdRequestMACFields("GET", "/v2/poll", "1", "nonce", thunderbirdBodyHash(nil))
	mac := thunderbirdMAC64(key, fields...)
	if !thunderbirdVerifyMAC(key, mac, fields...) {
		t.Fatal("valid MAC rejected")
	}
	for i := range fields {
		changed := append([]string(nil), fields...)
		changed[i] += "x"
		if thunderbirdVerifyMAC(key, mac, changed...) {
			t.Fatalf("field %d change accepted", i)
		}
	}
	if thunderbirdVerifyMAC(bytes.Repeat([]byte{2}, 32), mac, fields...) {
		t.Fatal("wrong key accepted")
	}
	for _, bad := range []string{"", "!!!", mac[:10], mac + "AA"} {
		if thunderbirdVerifyMAC(key, bad, fields...) {
			t.Fatalf("malformed MAC %q accepted", bad)
		}
	}
}

func TestThunderbirdTimestampAndNonceRules(t *testing.T) {
	now := time.Now().UnixMilli()
	for _, ok := range []int64{now, now - 59_000, now + 59_000} {
		if err := thunderbirdCheckTimestamp(itoa64(ok), now); err != nil {
			t.Fatalf("fresh %d rejected", ok)
		}
	}
	for _, bad := range []string{itoa64(now - 61_000), itoa64(now + 61_000), "", "abc", "1e3"} {
		if thunderbirdCheckTimestamp(bad, now) == nil {
			t.Fatalf("stale/malformed %q accepted", bad)
		}
	}
	if !thunderbirdValidNonce(thunderbirdRandomID(16)) || thunderbirdValidNonce("short") || thunderbirdValidNonce(strings.Repeat("!", 30)) {
		t.Fatal("nonce syntax rules")
	}
}

func itoa64(v int64) string { return strconv.FormatInt(v, 10) }

func TestThunderbirdStoreProtectsSecretAndRoundTrips(t *testing.T) {
	root := t.TempDir()
	store := thunderbirdStoreFor(root)
	hostID, pairing, err := store.load()
	if err != nil || hostID == "" || pairing != nil {
		t.Fatalf("first load: id=%q pairing=%v err=%v", hostID, pairing, err)
	}
	again, _, err := store.load()
	if err != nil || again != hostID {
		t.Fatalf("host id not persistent: %q vs %q (%v)", again, hostID, err)
	}
	secret := bytes.Repeat([]byte{0xA5}, 32)
	want := &thunderbirdPairing{ID: "p1", Key: secret, Origin: "moz-extension://abc", PairedAt: time.Now().UTC().Truncate(time.Second), ExtensionVersion: "0.10.0"}
	if err := store.save(hostID, want); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(store.path())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(b64.EncodeToString(secret))) && strings.Contains(string(raw), "dpapi:") {
		t.Fatal("DPAPI-protected file contains the raw secret")
	}
	if bytes.Contains(raw, secret) {
		t.Fatal("pairing file contains raw secret bytes")
	}
	_, got, err := store.load()
	if err != nil || got == nil || !bytes.Equal(got.Key, secret) || got.ID != "p1" || got.Origin != want.Origin {
		t.Fatalf("round trip: %+v err=%v", got, err)
	}
	// A damaged or foreign-account blob is reported, never used.
	damaged := strings.Replace(string(raw), `"protected_key": "`, `"protected_key": "x`, 1)
	if err := os.WriteFile(store.path(), []byte(damaged), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, p, err := store.load(); p != nil || err == nil {
		t.Fatalf("damaged secret accepted: %v %v", p, err)
	}
	if err := store.save(hostID, nil); err != nil {
		t.Fatal(err)
	}
	if _, p, err := store.load(); p != nil || err != nil {
		t.Fatalf("unpaired store: %v %v", p, err)
	}
	if entries, _ := filepath.Glob(filepath.Join(root, "thunderbird-bridge", "*.tmp")); len(entries) != 0 {
		t.Fatalf("temporary files left: %v", entries)
	}
}

func TestThunderbirdPairTranscriptBindsEveryField(t *testing.T) {
	base := []string{"h", "ep", "hp", "en", "hn"}
	ref := thunderbirdPairTranscript(base[0], base[1], base[2], base[3], base[4])
	for i := range base {
		v := append([]string(nil), base...)
		v[i] += "1"
		if thunderbirdPairTranscript(v[0], v[1], v[2], v[3], v[4]) == ref {
			t.Fatalf("field %d not bound", i)
		}
	}
	sum := sha256.Sum256([]byte("x"))
	if len(b64.EncodeToString(sum[:])) != 43 {
		t.Fatal("unexpected base64url length")
	}
}
