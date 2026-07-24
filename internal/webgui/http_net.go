package webgui

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

func isLoopbackHost(host string) bool {
	h := host
	if hostOnly, _, err := net.SplitHostPort(host); err == nil {
		h = hostOnly
	}
	h = strings.TrimSpace(strings.ToLower(h))
	if h == "localhost" || h == "" {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// isLoopbackRemoteAddr validates the actual TCP peer address. Unlike Host it
// cannot be supplied by a remote browser, so secret-returning handlers use it
// in addition to the DNS-rebinding Host check. Malformed and empty values fail
// closed.
func isLoopbackRemoteAddr(remoteAddr string) bool {
	addr, err := netip.ParseAddrPort(strings.TrimSpace(remoteAddr))
	if err != nil {
		return false
	}
	return addr.Addr().Unmap().IsLoopback()
}

// shutdownTimeout bounds graceful shutdown of the HTTP server.
const shutdownTimeout = 3 * time.Second

// Shutdown gracefully stops srv within shutdownTimeout.
func Shutdown(srv *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return srv.Shutdown(ctx)
}
