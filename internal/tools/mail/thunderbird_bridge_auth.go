package mail

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
)

// Thunderbird Bridge protocol 2 ("tb-bridge/2") cryptographic primitives.
//
// The bridge used to authenticate with a constant token compiled into both the
// host and the extension, so any local process (or a second SuperCli copy)
// could impersonate either side. Protocol 2 replaces it with a per-pairing
// 256-bit secret agreed once through a short pairing code, a mutual
// challenge-response on every connection and an HMAC over every request and
// response. All canonical strings are fields joined with "\n"; binary values
// travel as unpadded base64url. docs/PROTOCOL.md in Thunderbird-AI-Bridge is the
// normative description and the extension implements the same functions in
// extension/bridge-auth.js.

const (
	thunderbirdProtocolVersion = 2
	thunderbirdProtocolName    = "tb-bridge/2"
	// The code alphabet is Crockford base32: no I, L, O or U, so a code read
	// off the screen cannot be mistyped as a look-alike letter.
	thunderbirdCodeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	thunderbirdCodeLength   = 16 // 80 bits: far beyond offline guessing during the 5-minute window
	// Maximum clock skew for signed requests. Both sides run on one machine.
	thunderbirdMaxSkewMillis = 60_000
)

var b64 = base64.RawURLEncoding

func thunderbirdRandom(n int) []byte {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand never fails on supported platforms; a zero key would be
		// catastrophic, so refuse to continue rather than fall back.
		panic("thunderbird bridge: crypto/rand failed: " + err.Error())
	}
	return buf
}

func thunderbirdRandomID(n int) string { return b64.EncodeToString(thunderbirdRandom(n)) }

// thunderbirdNewPairingCode returns a fresh 16-character code shown to the user
// as XXXX-XXXX-XXXX-XXXX.
func thunderbirdNewPairingCode() string {
	raw := thunderbirdRandom(thunderbirdCodeLength)
	out := make([]byte, thunderbirdCodeLength)
	for i, b := range raw {
		out[i] = thunderbirdCodeAlphabet[int(b)&31]
	}
	return string(out)
}

func thunderbirdFormatPairingCode(code string) string {
	var parts []string
	for i := 0; i < len(code); i += 4 {
		end := min(i+4, len(code))
		parts = append(parts, code[i:end])
	}
	return strings.Join(parts, "-")
}

// thunderbirdNormalizePairingCode accepts what a person types: lower case,
// spaces, dashes and the look-alikes O/I/L.
func thunderbirdNormalizePairingCode(input string) (string, bool) {
	var out strings.Builder
	for _, r := range strings.ToUpper(input) {
		switch {
		case r == ' ' || r == '-' || r == '\t':
			continue
		case r == 'O':
			r = '0'
		case r == 'I' || r == 'L':
			r = '1'
		}
		if r > 127 || !strings.ContainsRune(thunderbirdCodeAlphabet, r) {
			return "", false
		}
		out.WriteRune(r)
	}
	if out.Len() != thunderbirdCodeLength {
		return "", false
	}
	return out.String(), true
}

// thunderbirdCodeKey derives the HMAC key that proves knowledge of the code.
func thunderbirdCodeKey(normalized string) []byte {
	sum := sha256.Sum256([]byte(thunderbirdProtocolName + " pairing code\n" + normalized))
	return sum[:]
}

func thunderbirdMAC(key []byte, fields ...string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(strings.Join(fields, "\n")))
	return mac.Sum(nil)
}

func thunderbirdMAC64(key []byte, fields ...string) string {
	return b64.EncodeToString(thunderbirdMAC(key, fields...))
}

// thunderbirdVerifyMAC compares in constant time; a malformed MAC is false.
func thunderbirdVerifyMAC(key []byte, got string, fields ...string) bool {
	raw, err := b64.DecodeString(got)
	if err != nil || len(raw) != sha256.Size {
		return false
	}
	return hmac.Equal(raw, thunderbirdMAC(key, fields...))
}

func thunderbirdBodyHash(body []byte) string {
	sum := sha256.Sum256(body)
	return b64.EncodeToString(sum[:])
}

// thunderbirdSessionKey binds a session to both challenges and its id.
func thunderbirdSessionKey(pairingKey []byte, hostNonce, extNonce, sessionID string) []byte {
	return thunderbirdMAC(pairingKey, "session", hostNonce, extNonce, sessionID)
}

// thunderbirdRequestMACFields is the canonical request string. The path
// includes the raw query, so transfer ids and filenames are authenticated.
func thunderbirdRequestMACFields(method, pathAndQuery, ts, nonce, bodyHash string) []string {
	return []string{"req", strings.ToUpper(method), pathAndQuery, ts, nonce, bodyHash}
}

// thunderbirdResponseMACFields binds a response to the request nonce, so an
// old response cannot be replayed for a new request.
func thunderbirdResponseMACFields(status int, requestNonce, ts, bodyHash string) []string {
	return []string{"res", strconv.Itoa(status), requestNonce, ts, bodyHash}
}

// thunderbirdValidNonce accepts 16-64 bytes of base64url randomness.
func thunderbirdValidNonce(nonce string) bool {
	if len(nonce) < 22 || len(nonce) > 86 {
		return false
	}
	_, err := b64.DecodeString(nonce)
	return err == nil
}

var errThunderbirdBadTimestamp = errors.New("timestamp outside the allowed window")

func thunderbirdCheckTimestamp(ts string, nowMillis int64) error {
	value, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return errThunderbirdBadTimestamp
	}
	if d := nowMillis - value; d > thunderbirdMaxSkewMillis || d < -thunderbirdMaxSkewMillis {
		return errThunderbirdBadTimestamp
	}
	return nil
}
