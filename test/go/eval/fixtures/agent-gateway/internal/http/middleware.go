package http

import (
	"net/http"

	"gateway/internal/auth"
	"gateway/internal/limits"
)

func withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := auth.KeyFrom(r); !ok {
			http.Error(w, "missing api key", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// withLimit rejects requests whose key has no tokens left.
func withLimit(b *limits.Buckets, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, _ := auth.KeyFrom(r)
		if !b.Allow(key) {
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
