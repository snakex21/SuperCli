package codexauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// refreshInterval mirrors the Codex CLI: tokens older than this
// are proactively refreshed before use.
const refreshInterval = 28 * 24 * time.Hour

// Manager loads, refreshes, and persists auth.json. It is safe
// for concurrent use and implements llm.CodexTokenSource.
type Manager struct {
	mu   sync.Mutex
	path string
	opts Options
	http *http.Client
}

// NewManager builds a Manager for <dataDir>/auth.json.
func NewManager(dataDir string, opts Options) *Manager {
	return &Manager{
		path: AuthFilePath(dataDir),
		opts: opts.WithDefaults(),
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

// SetHTTPClient overrides the HTTP client (tests).
func (m *Manager) SetHTTPClient(c *http.Client) { m.http = c }

// Path returns the auth.json path.
func (m *Manager) Path() string { return m.path }

// Options returns the resolved endpoint options.
func (m *Manager) Options() Options { return m.opts }

// LoggedIn reports whether a usable token set is on disk.
func (m *Manager) LoggedIn() bool {
	af, err := Load(m.path)
	return err == nil && af != nil && af.Tokens != nil && af.Tokens.AccessToken != ""
}

// Token returns a valid access token and the ChatGPT account id,
// refreshing stale tokens (older than 28 days) transparently.
func (m *Manager) Token(ctx context.Context) (access, accountID string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	af, err := Load(m.path)
	if err != nil {
		return "", "", err
	}
	if af == nil || af.Tokens == nil || af.Tokens.AccessToken == "" {
		return "", "", fmt.Errorf("codexauth: not logged in — run /login first")
	}
	if time.Since(af.LastRefresh) > refreshInterval && af.Tokens.RefreshToken != "" {
		if af, err = m.refreshLocked(ctx, af); err != nil {
			return "", "", err
		}
	}
	return af.Tokens.AccessToken, af.Tokens.AccountID, nil
}

// Refresh forces a token refresh (e.g. after a 401) and persists
// the result. Returns the new access token.
func (m *Manager) Refresh(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	af, err := Load(m.path)
	if err != nil {
		return "", err
	}
	if af == nil || af.Tokens == nil || af.Tokens.RefreshToken == "" {
		return "", fmt.Errorf("codexauth: no refresh token — run /login again")
	}
	af, err = m.refreshLocked(ctx, af)
	if err != nil {
		return "", err
	}
	return af.Tokens.AccessToken, nil
}

// Logout removes auth.json.
func (m *Manager) Logout() error { return Clear(m.path) }

// refreshLocked exchanges the refresh token at <issuer>/oauth/token
// using the JSON refresh request shape the Codex CLI uses
// ({client_id, grant_type:"refresh_token", refresh_token, scope}).
func (m *Manager) refreshLocked(ctx context.Context, af *AuthFile) (*AuthFile, error) {
	body, err := json.Marshal(map[string]string{
		"client_id":     m.opts.ClientID,
		"grant_type":    "refresh_token",
		"refresh_token": af.Tokens.RefreshToken,
		"scope":         "openid profile email",
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		trimSlash(m.opts.Issuer)+"/oauth/token", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("codexauth: refresh: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("codexauth: refresh failed (http %d): %s", resp.StatusCode, string(respBody))
	}
	var tok struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(respBody, &tok); err != nil {
		return nil, fmt.Errorf("codexauth: refresh parse: %w", err)
	}
	if tok.IDToken != "" {
		af.Tokens.IDToken = tok.IDToken
		if acc := ParseAccountID(tok.IDToken); acc != "" {
			af.Tokens.AccountID = acc
		}
	}
	if tok.AccessToken != "" {
		af.Tokens.AccessToken = tok.AccessToken
	}
	if tok.RefreshToken != "" {
		af.Tokens.RefreshToken = tok.RefreshToken
	}
	af.LastRefresh = time.Now()
	if err := Save(m.path, af); err != nil {
		return nil, err
	}
	return af, nil
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
