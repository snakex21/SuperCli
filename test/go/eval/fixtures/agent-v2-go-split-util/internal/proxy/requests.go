package proxy

import (
	u "gateway/internal/util"
)

// Request is a proxied request.
type Request struct {
	ID   string
	Path string
}

// NewRequest tags a request with an id.
func NewRequest(path string) Request {
	return Request{ID: u.NewID("req"), Path: path}
}
