package auth

import (
	"net/http"
	"strings"
)

// HeaderName carries the API key.
const HeaderName = "X-Api-Key"

// KeyFrom extracts the API key from the request.
func KeyFrom(r *http.Request) (string, bool) {
	k := strings.TrimSpace(r.Header.Get(HeaderName))
	return k, k != ""
}
