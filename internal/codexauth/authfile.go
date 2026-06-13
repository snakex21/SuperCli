package codexauth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Default endpoint values. These match the OpenAI Codex CLI
// reference implementation (codex-rs). They are compiled-in
// defaults only — config.toml's [codex_auth] section overrides
// every one of them.
const (
	// DefaultClientID is the public OAuth client id used by the
	// Codex CLI (codex-rs/login/src/auth/manager.rs CLIENT_ID).
	DefaultClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	// DefaultIssuer hosts /oauth/authorize and /oauth/token.
	DefaultIssuer = "https://auth.openai.com"
	// DefaultBackendURL is the ChatGPT backend Codex API root.
	// The Responses endpoint is <backend>/responses.
	DefaultBackendURL = "https://chatgpt.com/backend-api/codex"
)

// Options bundles the configurable endpoints. Zero fields fall
// back to the compiled-in defaults.
type Options struct {
	ClientID   string
	Issuer     string
	BackendURL string
}

// WithDefaults returns o with empty fields replaced by defaults.
func (o Options) WithDefaults() Options {
	if o.ClientID == "" {
		o.ClientID = DefaultClientID
	}
	if o.Issuer == "" {
		o.Issuer = DefaultIssuer
	}
	if o.BackendURL == "" {
		o.BackendURL = DefaultBackendURL
	}
	return o
}

// TokenData are the OAuth tokens persisted to auth.json.
type TokenData struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id,omitempty"`
}

// AuthFile is the on-disk auth.json structure. The layout is
// compatible in spirit with the Codex CLI's auth.json (tokens +
// last_refresh) so users recognize it.
type AuthFile struct {
	Tokens      *TokenData `json:"tokens,omitempty"`
	LastRefresh time.Time  `json:"last_refresh,omitempty"`
}

// AuthFilePath returns <dataDir>/auth.json, where dataDir is the
// resolved SuperCli data directory (portable default: supercli-data
// next to the executable).
func AuthFilePath(dataDir string) string {
	return filepath.Join(dataDir, "auth.json")
}

// Load reads auth.json. A missing file returns (nil, nil).
func Load(path string) (*AuthFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("codexauth: read %s: %w", path, err)
	}
	var af AuthFile
	if err := json.Unmarshal(data, &af); err != nil {
		return nil, fmt.Errorf("codexauth: parse %s: %w", path, err)
	}
	return &af, nil
}

// Save writes auth.json with 0600 permissions, creating the
// parent directory if needed.
func Save(path string, af *AuthFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("codexauth: mkdir: %w", err)
	}
	data, err := json.MarshalIndent(af, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("codexauth: write %s: %w", path, err)
	}
	// Re-apply permissions in case the file already existed.
	_ = os.Chmod(path, 0o600)
	return nil
}

// Clear removes auth.json. Missing file is not an error.
func Clear(path string) error {
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ParseAccountID extracts the ChatGPT account id from the id
// token's "https://api.openai.com/auth" claim object — the same
// claim the Codex CLI reads (chatgpt_account_id).
func ParseAccountID(idToken string) string {
	claims := jwtAuthClaims(idToken)
	if v, ok := claims["chatgpt_account_id"].(string); ok {
		return v
	}
	return ""
}

// ParsePlanType extracts chatgpt_plan_type from a JWT (usually
// the access token). Empty when absent.
func ParsePlanType(token string) string {
	claims := jwtAuthClaims(token)
	if v, ok := claims["chatgpt_plan_type"].(string); ok {
		return v
	}
	return ""
}

// jwtAuthClaims decodes the JWT payload (without verifying the
// signature — we only mine display/routing metadata from it) and
// returns the nested "https://api.openai.com/auth" claim object.
func jwtAuthClaims(jwt string) map[string]any {
	parts := splitJWT(jwt)
	if parts == nil {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var v map[string]any
	if err := json.Unmarshal(payload, &v); err != nil {
		return nil
	}
	if obj, ok := v["https://api.openai.com/auth"].(map[string]any); ok {
		return obj
	}
	return nil
}

func splitJWT(jwt string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(jwt); i++ {
		if jwt[i] == '.' {
			parts = append(parts, jwt[start:i])
			start = i + 1
		}
	}
	parts = append(parts, jwt[start:])
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil
	}
	return parts
}
