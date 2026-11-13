package llm

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// openCodeZenRawTransport sends HTTP/1.1 requests in the EXACT byte shape of
// the official opencode CLI (bun/undici) over the utls ClientHello connection.
//
// Why a raw writer instead of *http.Transport: the Zen free-tier edge
// fingerprints the HTTP request bytes in addition to the TLS handshake
// (bisected 2026-10-31). Go's net/http cannot reproduce bun's wire shape:
//
//   - it canonicalizes header names (x-opencode-client -> X-Opencode-Client);
//   - it sorts headers and hoists Content-Length to position 3, while bun
//     appends Content-Length LAST;
//   - it advertises only "Accept-Encoding: gzip" (and transparently inflates),
//     while bun sends "gzip, deflate, br, zstd".
//
// The IDENTICAL body with bun's header bytes returns 200; the same body with
// Go's header bytes returns a fake FreeUsageLimitError 429. So for Zen we
// bypass net/http's request writer and emit the proven bytes ourselves, then
// parse the response with http.ReadResponse so streaming SSE still works.
//
// Scope: installed ONLY for opencode.ai Zen base URLs (see
// newOpenCodeZenHTTPClient). Every other backend keeps Go's stock transport.
type openCodeZenRawTransport struct {
	// dialTLS perfs the bun ClientHello handshake (see zen_fingerprint_transport.go).
	dialTLS func(ctx context.Context, network, addr string) (net.Conn, error)
}

// zenExchange owns one request/response socket: it closes the connection on
// context cancellation (unblocking a pending header read or a stalled SSE
// stream) and exactly once when the response body is closed.
type zenExchange struct {
	conn      net.Conn
	closeOnce sync.Once
	watchStop chan struct{}
	stopOnce  sync.Once
}

func (e *zenExchange) stopWatch() { e.stopOnce.Do(func() { close(e.watchStop) }) }
func (e *zenExchange) closeConn() { e.closeOnce.Do(func() { _ = e.conn.Close() }) }

// watchCtx closes the socket the moment the request context is canceled so a
// blocked ReadResponse/Read returns instead of hanging until the watchdog.
func (e *zenExchange) watchCtx(ctx context.Context) {
	go func() {
		select {
		case <-ctx.Done():
			e.closeConn()
		case <-e.watchStop:
		}
	}()
}

// zenConnBody ties the response body lifetime to the socket: closing it stops
// the context watcher and closes the connection exactly once.
type zenConnBody struct {
	body io.ReadCloser
	exch *zenExchange
	once sync.Once
	err  error
}

func (b *zenConnBody) Read(p []byte) (int, error) { return b.body.Read(p) }

func (b *zenConnBody) Close() error {
	b.once.Do(func() {
		b.exch.stopWatch()
		b.err = b.body.Close()
		b.exch.closeConn()
	})
	return b.err
}

// zenDecompressBody inflates a Content-Encoding'd body while keeping the
// underlying socket lifetime (via raw) intact.
type zenDecompressBody struct {
	decomp io.ReadCloser
	raw    io.ReadCloser
}

func (b *zenDecompressBody) Read(p []byte) (int, error) { return b.decomp.Read(p) }

func (b *zenDecompressBody) Close() error {
	err := b.decomp.Close()
	if rerr := b.raw.Close(); err == nil {
		err = rerr
	}
	return err
}

// wrapZenContentEncoding returns a decompressing reader for the response
// Content-Encoding, or the body unchanged for identity/unknown encodings. The
// full bun Accept-Encoding list is advertised, so all four must be decodable.
func wrapZenContentEncoding(enc string, body io.ReadCloser) (io.ReadCloser, bool) {
	switch strings.ToLower(strings.TrimSpace(enc)) {
	case "", "identity":
		return body, false
	case "gzip":
		gz, err := gzip.NewReader(body)
		if err != nil {
			return body, false
		}
		return &zenDecompressBody{decomp: gz, raw: body}, true
	case "deflate":
		return &zenDecompressBody{decomp: flate.NewReader(body), raw: body}, true
	case "br":
		// brotli.Reader has no Close; the raw socket body owns teardown.
		return &zenDecompressBody{decomp: io.NopCloser(brotli.NewReader(body)), raw: body}, true
	case "zstd":
		dec, err := zstd.NewReader(body)
		if err != nil {
			return body, false
		}
		return &zenDecompressBody{decomp: dec.IOReadCloser(), raw: body}, true
	}
	return body, false
}

// RoundTrip writes the request in bun's exact header order over a fresh utls
// socket and parses the response, keeping the socket tied to the response body.
func (t *openCodeZenRawTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	addr := req.URL.Host
	if req.URL.Port() == "" {
		addr = net.JoinHostPort(req.URL.Hostname(), "443")
	}
	conn, err := t.dialTLS(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	exch := &zenExchange{conn: conn, watchStop: make(chan struct{})}
	exch.watchCtx(ctx)

	// RoundTrip must always close req.Body, including on error.
	var body []byte
	if req.Body != nil {
		body, err = io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			exch.stopWatch()
			exch.closeConn()
			return nil, err
		}
	}

	if err := writeZenRawRequest(conn, req, body); err != nil {
		exch.stopWatch()
		exch.closeConn()
		return nil, err
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		exch.stopWatch()
		exch.closeConn()
		return nil, err
	}

	base := &zenConnBody{body: resp.Body, exch: exch}
	if wrapped, ok := wrapZenContentEncoding(resp.Header.Get("Content-Encoding"), base); ok {
		// Mirror net/http: after inflating, drop the encoding markers so
		// downstream JSON/SSE parsers see plain bytes.
		resp.Header.Del("Content-Encoding")
		resp.ContentLength = -1
		resp.Uncompressed = true
		resp.Body = wrapped
	} else {
		resp.Body = base
	}
	return resp, nil
}

// zenSanitizeHeaderValue strips CR/LF so a value can never split the request
// (header injection). Values here are machine-generated, but defense in depth.
func zenSanitizeHeaderValue(v string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, v)
}

// writeZenRawRequest emits the request line and headers in the exact order the
// official bun CLI uses (verified 200 against the live Zen edge 2026-10-31):
//
//	POST <path> HTTP/1.1
//	Host, User-Agent, Accept, Accept-Encoding, Authorization, Connection,
//	Content-Type, x-opencode-*, Content-Length   <- Content-Length LAST
//
// Header names for the x-opencode-* family are lowercase to match bun.
func writeZenRawRequest(w io.Writer, req *http.Request, body []byte) error {
	var buf bytes.Buffer
	path := req.URL.RequestURI()
	if path == "" {
		path = "/"
	}
	fmt.Fprintf(&buf, "%s %s HTTP/1.1\r\n", req.Method, path)
	fmt.Fprintf(&buf, "Host: %s\r\n", zenSanitizeHeaderValue(req.URL.Host))

	emitted := map[string]bool{"host": true}
	add := func(key, wireKey, val string) {
		lk := strings.ToLower(key)
		if val == "" || emitted[lk] {
			return
		}
		emitted[lk] = true
		fmt.Fprintf(&buf, "%s: %s\r\n", wireKey, zenSanitizeHeaderValue(val))
	}

	add("User-Agent", "User-Agent", req.Header.Get("User-Agent"))
	add("Accept", "Accept", req.Header.Get("Accept"))
	// bun's exact Accept-Encoding list (not Go's gzip-only default).
	buf.WriteString("Accept-Encoding: gzip, deflate, br, zstd\r\n")
	emitted["accept-encoding"] = true
	add("Authorization", "Authorization", req.Header.Get("Authorization"))
	buf.WriteString("Connection: keep-alive\r\n")
	emitted["connection"] = true
	add("Content-Type", "Content-Type", req.Header.Get("Content-Type"))

	// x-opencode-* in lowercase, in bun's order. The map is keyed lowercase
	// so Go's canonical form (X-Opencode-Client) matches and the extra pass
	// below does not re-emit them.
	for _, k := range []string{"X-OpenCode-Client", "X-OpenCode-Project", "X-OpenCode-Request", "X-OpenCode-Session", "X-OpenCode-Session-Id"} {
		add(k, strings.ToLower(k), req.Header.Get(k))
	}

	// Any other caller-supplied header, appended before Content-Length so
	// nothing is silently dropped (no-op for the OpenAI-chat Zen path).
	var extra []string
	for k := range req.Header {
		if !emitted[strings.ToLower(k)] {
			extra = append(extra, k)
		}
	}
	if len(extra) > 0 {
		sortStrings(extra)
		for _, k := range extra {
			fmt.Fprintf(&buf, "%s: %s\r\n", zenSanitizeHeaderValue(k), zenSanitizeHeaderValue(req.Header.Get(k)))
		}
	}

	fmt.Fprintf(&buf, "Content-Length: %d\r\n\r\n", len(body))
	if _, err := w.Write(buf.Bytes()); err != nil {
		return err
	}
	if len(body) > 0 {
		if _, err := w.Write(body); err != nil {
			return err
		}
	}
	return nil
}

// sortStrings is a tiny insertion sort (header counts are small) to avoid an
// extra import for one call site.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// zenDialOverride, when non-nil, replaces the utls ClientHello dialer in
// newOpenCodeZenHTTPClient. The raw transport dials opencode.ai directly (it
// must, to replay bun's handshake), which would otherwise bypass a test that
// intercepts via http.DefaultTransport — so tests register their fixture dial
// here instead. Production leaves it nil. Guarded because tests set it while
// request builders may run concurrently.
var (
	zenDialOverrideMu sync.Mutex
	zenDialOverride   func(ctx context.Context, network, addr string) (net.Conn, error)
)

// SetZenDialOverride swaps the Zen transport's TLS dialer (nil restores the
// default utls replay). Exported for tests that need to point Zen traffic at a
// local fixture without a cloud request.
func SetZenDialOverride(fn func(ctx context.Context, network, addr string) (net.Conn, error)) {
	zenDialOverrideMu.Lock()
	defer zenDialOverrideMu.Unlock()
	zenDialOverride = fn
}

// newOpenCodeZenHTTPClient returns an *http.Client that speaks the official
// CLI's exact wire bytes (utls ClientHello + raw bun-shaped HTTP) when baseURL
// is a Zen free-tier endpoint, or nil for every other backend so the caller
// keeps Go's stock transport. connectTimeout bounds the TCP dial only.
func newOpenCodeZenHTTPClient(baseURL string, connectTimeout time.Duration) *http.Client {
	if !isOpenCodeZenBaseURL(baseURL) {
		return nil
	}
	if connectTimeout <= 0 {
		connectTimeout = 30 * time.Second
	}
	dial := openCodeZenDialTLSContext(connectTimeout)
	zenDialOverrideMu.Lock()
	if zenDialOverride != nil {
		dial = zenDialOverride
	}
	zenDialOverrideMu.Unlock()
	return &http.Client{
		Transport: &openCodeZenRawTransport{
			dialTLS: dial,
		},
		// No Client.Timeout: the streaming body must not be capped.
	}
}
