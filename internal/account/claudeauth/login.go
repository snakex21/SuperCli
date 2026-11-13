package claudeauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"
)

// Login waits for one browser callback. Success is shown only after both the
// token exchange and the portable credential save have completed.
func (m *Manager) Login(ctx context.Context, open func(string) error) error {
	if m.err != nil {
		return m.err
	}
	if open == nil {
		return fmt.Errorf("Claude: browser opener is required")
	}
	if !m.state.login.TryLock() {
		return fmt.Errorf("Claude login is already in progress")
	}
	defer m.state.login.Unlock()
	m.state.mu.Lock()
	generation := m.state.generation
	m.state.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	random := make([]byte, 64)
	if _, err := rand.Read(random); err != nil {
		return err
	}
	verifier := base64.RawURLEncoding.EncodeToString(random)
	digest := sha256.Sum256([]byte(verifier))
	if _, err := rand.Read(random); err != nil {
		return err
	}
	state := base64.RawURLEncoding.EncodeToString(random)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("Claude: cannot start login callback: %w", err)
	}
	defer listener.Close()
	redirect := fmt.Sprintf("http://localhost:%d/callback", listener.Addr().(*net.TCPAddr).Port)
	u, err := url.Parse(m.opts.AuthorizeURL)
	if err != nil {
		return err
	}
	q := u.Query()
	for k, v := range map[string]string{
		"code": "true", "client_id": m.opts.ClientID, "response_type": "code", "redirect_uri": redirect,
		"scope": "user:profile user:inference", "code_challenge": base64.RawURLEncoding.EncodeToString(digest[:]),
		"code_challenge_method": "S256", "state": state,
	} {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	result := make(chan error, 1)
	var received atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("state")), []byte(state)) != 1 {
			writeCallbackPage(w, m.opts, false, http.StatusBadRequest)
			return
		}
		if !received.CompareAndSwap(false, true) {
			http.Error(w, "Login callback already received", http.StatusConflict)
			return
		}
		var loginErr error
		defer func() { result <- loginErr }()
		code := r.URL.Query().Get("code")
		if r.URL.Query().Get("error") != "" || code == "" {
			loginErr = fmt.Errorf("Claude login was declined or returned no authorization code")
		} else {
			var tokens Tokens
			tokens, loginErr = m.exchange(ctx, map[string]any{
				"grant_type": "authorization_code", "code": code, "state": state,
				"redirect_uri": redirect, "client_id": m.opts.ClientID, "code_verifier": verifier,
			})
			if loginErr == nil {
				tokens.LoginID = state
				m.state.mu.Lock()
				if generation != m.state.generation {
					loginErr = fmt.Errorf("Claude login cancelled by logout")
				} else {
					loginErr = m.save(tokens)
				}
				m.state.mu.Unlock()
			}
		}
		status := http.StatusOK
		if loginErr != nil {
			status = http.StatusBadRequest
		}
		writeCallbackPage(w, m.opts, loginErr == nil, status)
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	if err := open(u.String()); err != nil {
		return fmt.Errorf("Claude: open login page: %w", err)
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("Claude login cancelled or timed out: %w", ctx.Err())
	case err := <-serveErr:
		return fmt.Errorf("Claude login callback stopped: %w", err)
	case err := <-result:
		return err
	}
}
