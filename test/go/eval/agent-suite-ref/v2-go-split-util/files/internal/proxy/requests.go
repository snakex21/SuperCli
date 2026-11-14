package proxy

import (
	"gateway/internal/ids"
)

// Request is a proxied request.
type Request struct {
	ID   string
	Path string
}

// NewRequest tags a request with an id.
func NewRequest(path string) Request {
	return Request{ID: ids.New("req"), Path: path}
}
