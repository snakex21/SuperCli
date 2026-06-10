// Package codexauth implements ChatGPT-subscription ("Codex")
// authentication for SuperCli: an OAuth 2.0 + PKCE browser login
// against auth.openai.com, token persistence in
// <home>/.supercli/auth.json (0600), transparent refresh, and a
// token source used by the codex LLM provider to authorize
// requests to the ChatGPT backend Responses API.
//
// The flow is a Go port of the OpenAI Codex CLI's Rust login
// implementation (codex-rs/login). Endpoints, the OAuth client
// ID, and the backend base URL are configurable via the
// [codex_auth] section of config.toml; the compiled-in defaults
// match the reference values.
package codexauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// PKCECodes holds an OAuth PKCE verifier/challenge pair.
type PKCECodes struct {
	CodeVerifier  string
	CodeChallenge string
}

// GeneratePKCE creates a fresh PKCE pair: the verifier is 64
// random bytes base64url-encoded (no padding); the challenge is
// the S256 transform (base64url(SHA-256(verifier))). This mirrors
// codex-rs/login/src/pkce.rs.
func GeneratePKCE() (PKCECodes, error) {
	raw := make([]byte, 64)
	if _, err := rand.Read(raw); err != nil {
		return PKCECodes{}, err
	}
	verifier := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	return PKCECodes{
		CodeVerifier:  verifier,
		CodeChallenge: base64.RawURLEncoding.EncodeToString(sum[:]),
	}, nil
}

// generateState returns a random URL-safe state string for the
// OAuth authorize request.
func generateState() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
