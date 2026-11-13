package http

import (
	"net/http"

	"gateway/internal/auth"
	"gateway/internal/config"
	"gateway/internal/limits"
	"gateway/internal/proxy"
)

// NewRouter wires authentication, rate limiting and the upstream proxy.
func NewRouter(cfg config.Config) http.Handler {
	buckets := limits.NewBuckets(cfg.RateLimitPerMinute, cfg.Burst)
	mux := http.NewServeMux()
	mux.Handle("/v1/", withAuth(withLimit(buckets, proxy.New("http://upstream:9000"))))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	_ = auth.HeaderName
	return mux
}
