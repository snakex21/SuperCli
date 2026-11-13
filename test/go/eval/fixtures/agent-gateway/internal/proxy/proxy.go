package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"
)

// New returns a reverse proxy to target.
func New(target string) http.Handler {
	u, _ := url.Parse(target)
	return httputil.NewSingleHostReverseProxy(u)
}
