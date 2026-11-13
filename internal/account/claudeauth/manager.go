// Package claudeauth keeps Claude OAuth credentials in SuperCli's portable data
// directory. It never reads another application's credentials or a user profile.
package claudeauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	ClientID     = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	AuthorizeURL = "https://claude.ai/oauth/authorize"
	TokenURL     = "https://platform.claude.com/v1/oauth/token"
	BaseURL      = "https://api.anthropic.com/v1"
	Beta         = "oauth-2025-04-20"
)

type Options struct {
	ClientID     string
	AuthorizeURL string
	TokenURL     string
	HTTPClient   *http.Client
	UsageURL     string
	Language     string
	ReturnURL    string // validated loopback application URL shown after browser login
}

type Tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	AccountID    string    `json:"account_id,omitempty"`
	LoginID      string    `json:"login_id,omitempty"`
}

type state struct {
	mu         sync.Mutex
	login      sync.Mutex
	usage      sync.Mutex
	generation uint64
}

var states sync.Map

type Manager struct {
	path  string
	opts  Options
	state *state
	err   error
}

func NewManager(dataDir string, opts Options) *Manager {
	if opts.ClientID == "" {
		opts.ClientID = ClientID
	}
	if opts.AuthorizeURL == "" {
		opts.AuthorizeURL = AuthorizeURL
	}
	if opts.TokenURL == "" {
		opts.TokenURL = TokenURL
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	m := &Manager{opts: opts}
	if dataDir == "" {
		m.err = fmt.Errorf("Claude login requires the SuperCli data directory")
		m.state = &state{}
		return m
	}
	m.path, m.err = filepath.Abs(filepath.Join(dataDir, "claude-auth.json"))
	s, _ := states.LoadOrStore(m.path, &state{})
	m.state = s.(*state)
	return m
}

func (m *Manager) load() (Tokens, error) {
	if m.err != nil {
		return Tokens{}, m.err
	}
	b, err := os.ReadFile(m.path)
	if os.IsNotExist(err) {
		return Tokens{}, fmt.Errorf("Claude: not logged in; use /login claude")
	}
	if err != nil {
		return Tokens{}, fmt.Errorf("Claude: read credentials: %w", err)
	}
	var tokens Tokens
	if err := json.Unmarshal(bytes.TrimPrefix(b, []byte{0xef, 0xbb, 0xbf}), &tokens); err != nil {
		return Tokens{}, fmt.Errorf("Claude: invalid credentials file")
	}
	if tokens.AccessToken == "" {
		return Tokens{}, fmt.Errorf("Claude: not logged in; use /login claude")
	}
	return tokens, nil
}

func (m *Manager) LoggedIn() bool {
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	t, err := m.load()
	return err == nil && (t.RefreshToken != "" || time.Now().Before(t.ExpiresAt))
}

func (m *Manager) save(tokens Tokens) error {
	if m.err != nil {
		return m.err
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0700); err != nil {
		return fmt.Errorf("Claude: create portable credentials directory: %w", err)
	}
	b, err := json.MarshalIndent(tokens, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(m.path), ".claude-auth-*")
	if err != nil {
		return fmt.Errorf("Claude: write portable credentials: %w", err)
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), m.path)
}

func (m *Manager) Logout() error {
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.state.generation++
	_ = os.Remove(filepath.Join(filepath.Dir(m.path), "claude-usage.json"))
	err := os.Remove(m.path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// AccessToken serializes refresh across all managers for the same portable
// credential file, including model discovery and simultaneous agent calls.
func (m *Manager) AccessToken(ctx context.Context, force bool) (string, error) {
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	t, err := m.load()
	if err != nil {
		return "", err
	}
	if !force && time.Now().Add(time.Minute).Before(t.ExpiresAt) {
		return t.AccessToken, nil
	}
	if t.RefreshToken == "" {
		return "", fmt.Errorf("Claude session expired; use /login claude")
	}
	next, err := m.exchange(ctx, map[string]any{
		"grant_type": "refresh_token", "refresh_token": t.RefreshToken, "client_id": m.opts.ClientID,
	})
	if err != nil {
		return "", err
	}
	if next.RefreshToken == "" {
		next.RefreshToken = t.RefreshToken
	}
	next.LoginID = t.LoginID
	if next.AccountID == "" {
		next.AccountID = t.AccountID
	}
	if next.LoginID == "" {
		next.LoginID = "sha256:" + tokenIdentity(t)
	}
	if err := m.save(next); err != nil {
		return "", err
	}
	return next.AccessToken, nil
}

func (m *Manager) exchange(ctx context.Context, payload map[string]any) (Tokens, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return Tokens{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.opts.TokenURL, bytes.NewReader(b))
	if err != nil {
		return Tokens{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.opts.HTTPClient.Do(req)
	if err != nil {
		return Tokens{}, fmt.Errorf("Claude: token exchange unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Token responses may echo secrets; never put their body in logs or UI.
		return Tokens{}, fmt.Errorf("Claude: token exchange rejected (HTTP %d); sign in again", resp.StatusCode)
	}
	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Account      struct {
			UUID string `json:"uuid"`
		} `json:"account"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil || result.AccessToken == "" || result.ExpiresIn <= 0 || result.ExpiresIn > 366*24*60*60 {
		return Tokens{}, fmt.Errorf("Claude: invalid token response")
	}
	return Tokens{AccessToken: result.AccessToken, RefreshToken: result.RefreshToken, AccountID: result.Account.UUID, ExpiresAt: time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)}, nil
}
