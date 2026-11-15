// Package httpapi serves blobs over HTTP.
package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"blobhub/storage"
)

// Handler serves GET /blobs/<key>.
type Handler struct {
	Store storage.Store
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/blobs/")
	if r.Method != http.MethodGet || key == "" || key == r.URL.Path {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	b, err := h.Store.Get(r.Context(), key)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case err != nil:
		http.Error(w, "storage error", http.StatusBadGateway)
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(b)
	}
}
