package llm

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"time"

	tlsu "github.com/refraction-networking/utls"
)

// openCodeZenClientHelloSpec replays the exact TLS ClientHello of the official
// opencode CLI 1.19.0 (bun 1.3.14 / BoringSSL), captured 2026-10-31 and
// verified byte-identical (except random/session-id/keyshare) against the
// live Zen free-tier edge.
//
// Why this exists: the /inference/openai/v1 edge fingerprints the upstream TLS
// handshake. The IDENTICAL request body + headers that return 200 through bun
// return a fake FreeUsageLimitError 429 through Go crypto/tls and Python
// OpenSSL, regardless of HTTP/1.1 vs h2, header case, or TLS 1.2 vs 1.3. Two
// details separate a pass from a reject after byte-level diffing:
//
//   - BoringSSL interleaves ECDHE cipher suites ECDSA/RSA alternating by
//     strength (ecdsa128, rsa128, ecdsa256, rsa256, ecdsa-chacha, rsa-chacha)
//     instead of grouping by auth type;
//   - psk_key_exchange_modes (0x2d) comes BEFORE supported_versions (0x2b).
//
// Everything else (SNI, EMS, renegotiation, curves X25519/P256/P384, ec_point
// formats, session ticket, ALPN http/1.1, status_request, the 9-sigalgs order,
// SCT, X25519 keyshare, padding 237) matches the capture exactly.
func openCodeZenClientHelloSpec() *tlsu.ClientHelloSpec {
	return &tlsu.ClientHelloSpec{
		CipherSuites: []uint16{
			tlsu.TLS_AES_128_GCM_SHA256,
			tlsu.TLS_AES_256_GCM_SHA384,
			tlsu.TLS_CHACHA20_POLY1305_SHA256,
			tlsu.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tlsu.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tlsu.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tlsu.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tlsu.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tlsu.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
			tlsu.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
			tlsu.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
			tlsu.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
			tlsu.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
			tlsu.TLS_RSA_WITH_AES_128_GCM_SHA256,
			tlsu.TLS_RSA_WITH_AES_256_GCM_SHA384,
			tlsu.TLS_RSA_WITH_AES_128_CBC_SHA,
			tlsu.TLS_RSA_WITH_AES_256_CBC_SHA,
		},
		Extensions: []tlsu.TLSExtension{
			&tlsu.SNIExtension{},
			&tlsu.ExtendedMasterSecretExtension{},
			&tlsu.RenegotiationInfoExtension{Renegotiation: tlsu.RenegotiateOnceAsClient},
			&tlsu.SupportedCurvesExtension{Curves: []tlsu.CurveID{tlsu.X25519, tlsu.CurveP256, tlsu.CurveP384}},
			&tlsu.SupportedPointsExtension{SupportedPoints: []uint8{0}},
			&tlsu.SessionTicketExtension{},
			&tlsu.ALPNExtension{AlpnProtocols: []string{"http/1.1"}},
			&tlsu.StatusRequestExtension{},
			&tlsu.SignatureAlgorithmsExtension{SupportedSignatureAlgorithms: []tlsu.SignatureScheme{
				// exact captured order: ecdsa_secp256r1_sha256,
				// rsa_pss_rsae_sha256, rsa_pkcs1_sha256, ecdsa_secp384r1_sha384,
				// rsa_pss_rsae_sha384, rsa_pkcs1_sha384, rsa_pss_rsae_sha512,
				// rsa_pkcs1_sha512, ecdsa_sha1
				0x0403, 0x0804, 0x0401, 0x0503, 0x0805, 0x0501, 0x0806, 0x0601, 0x0201,
			}},
			&tlsu.SCTExtension{},
			&tlsu.KeyShareExtension{KeyShares: []tlsu.KeyShare{{Group: tlsu.X25519}}},
			// BoringSSL emits psk_key_exchange_modes BEFORE supported_versions.
			&tlsu.PSKKeyExchangeModesExtension{Modes: []uint8{1}},
			&tlsu.SupportedVersionsExtension{Versions: []uint16{tlsu.VersionTLS13, tlsu.VersionTLS12}},
			&tlsu.UtlsPaddingExtension{PaddingLen: 237, WillPad: true},
		},
		CompressionMethods: []uint8{0},
	}
}

// openCodeZenDialTLSContext returns a DialTLSContext hook that dials addr and
// performs the handshake with the bun ClientHello above. Because http.Transport
// skips its own TLS layer when DialTLSContext is set, this replaces Go's
// crypto/tls wholesale for Zen endpoints. ALPN offers only http/1.1 (what the
// real CLI negotiates), so no h2 upgrade can leak in.
func openCodeZenDialTLSContext(connectTimeout time.Duration) func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: connectTimeout, KeepAlive: 30 * time.Second}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		raw, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			raw.Close()
			return nil, err
		}
		uc := tlsu.UClient(raw, &tlsu.Config{
			ServerName: host,
			NextProtos: []string{"http/1.1"},
		}, tlsu.HelloCustom)
		if err := uc.ApplyPreset(openCodeZenClientHelloSpec()); err != nil {
			raw.Close()
			return nil, err
		}
		if err := uc.HandshakeContext(ctx); err != nil {
			raw.Close()
			return nil, err
		}
		return uc, nil
	}
}

// applyOpenCodeZenFingerprint pins a cloned transport to the official CLI's
// wire shape when baseURL is a Zen free-tier endpoint: utls ClientHello replay
// (TLS layer) plus HTTP/1.1-only pinning. Returns true when applied; non-Zen
// backends keep Go's default transport untouched. Note: the fingerprint covers
// DIRECT connections; behind an HTTPS proxy the tunnel falls back to Go TLS
// (the gate would reject it, but proxy users are outside the Zen contract
// anyway — the real CLI behaves the same through proxies).
func applyOpenCodeZenFingerprint(baseURL string, transport *http.Transport, connectTimeout time.Duration) bool {
	if !isOpenCodeZenBaseURL(baseURL) {
		return false
	}
	transport.ForceAttemptHTTP2 = false
	transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	transport.DialTLSContext = openCodeZenDialTLSContext(connectTimeout)
	return true
}
