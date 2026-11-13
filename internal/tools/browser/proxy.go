package browser

import (
	"context"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// dialFunc opens an upstream connection for the browser's proxy.
type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// guardProxy is a minimal in-process HTTP/CONNECT proxy. The headless
// browser sends every request through it (including redirects, subresources,
// fetch() and form posts), and its dialer refuses private, loopback and
// link-local addresses after resolving the name itself. Checking only the
// top-level URL would leave DNS rebinding and in-page requests unguarded.
type guardProxy struct {
	ln      net.Listener
	srv     *http.Server
	dial    dialFunc
	mu      sync.Mutex
	tunnels map[net.Conn]struct{}
}

func startGuardProxy(dial dialFunc) (*guardProxy, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &guardProxy{ln: ln, dial: dial, tunnels: map[net.Conn]struct{}{}}
	transport := &http.Transport{
		DialContext:           dial,
		Proxy:                 nil,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConnsPerHost:   4,
	}
	p.srv = &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodConnect {
				p.tunnel(w, r)
				return
			}
			p.forward(transport, w, r)
		}),
	}
	go func() { _ = p.srv.Serve(ln) }()
	return p, nil
}

func (p *guardProxy) addr() string { return p.ln.Addr().String() }

func (p *guardProxy) tunnel(w http.ResponseWriter, r *http.Request) {
	upstream, err := p.dial(r.Context(), "tcp", r.Host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		upstream.Close()
		http.Error(w, "proxy cannot tunnel", http.StatusInternalServerError)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		upstream.Close()
		return
	}
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		client.Close()
		upstream.Close()
		return
	}
	p.track(client, upstream)
	go func() {
		if buffered.Reader.Buffered() > 0 {
			_, _ = io.CopyN(upstream, buffered, int64(buffered.Reader.Buffered()))
		}
		_, _ = io.Copy(upstream, client)
		p.untrack(client, upstream)
	}()
	go func() {
		_, _ = io.Copy(client, upstream)
		p.untrack(client, upstream)
	}()
}

func (p *guardProxy) forward(transport *http.Transport, w http.ResponseWriter, r *http.Request) {
	if r.URL.Scheme != "http" || r.URL.Host == "" {
		http.Error(w, "only absolute http URLs are proxied", http.StatusBadRequest)
		return
	}
	out := r.Clone(r.Context())
	out.RequestURI = ""
	out.Header.Del("Proxy-Connection")
	out.Header.Del("Proxy-Authorization")
	resp, err := transport.RoundTrip(out)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for key, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (p *guardProxy) track(conns ...net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range conns {
		p.tunnels[c] = struct{}{}
	}
}

func (p *guardProxy) untrack(conns ...net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range conns {
		if _, ok := p.tunnels[c]; ok {
			delete(p.tunnels, c)
			c.Close()
		}
	}
}

func (p *guardProxy) Close() {
	_ = p.srv.Close()
	p.mu.Lock()
	defer p.mu.Unlock()
	for c := range p.tunnels {
		c.Close()
		delete(p.tunnels, c)
	}
}
