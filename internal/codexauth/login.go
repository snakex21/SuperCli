package codexauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Default callback ports — keep in sync with the Codex CLI's
// Hydra redirect-URI allow-list (1455 primary, 1457 fallback).
const (
	defaultCallbackPort  = 1455
	fallbackCallbackPort = 1457
)

// ErrHeadless is returned when an interactive browser login is
// requested in an environment without a display/TTY.
var ErrHeadless = errors.New(
	"codexauth: interactive browser login is unavailable in headless/batch mode; " +
		"run /login from an interactive terminal with a browser, or configure an API-key provider instead")

// LoginResult reports a successful login.
type LoginResult struct {
	AccountID string
	PlanType  string
}

// Login runs the full OAuth 2.0 + PKCE authorization-code flow:
//
//  1. bind a loopback callback server on 127.0.0.1:1455 (1457 fallback)
//  2. open the browser at <issuer>/oauth/authorize with a PKCE
//     challenge and random state
//  3. receive the code on /auth/callback, verify state
//  4. exchange the code for id/access/refresh tokens at
//     <issuer>/oauth/token
//  5. persist them to auth.json (0600)
//
// status (may be nil) receives progress lines, including the
// auth URL so the user can open it manually if the browser does
// not launch. The flow aborts when ctx is cancelled.
func (m *Manager) Login(ctx context.Context, status io.Writer) (*LoginResult, error) {
	if status == nil {
		status = io.Discard
	}
	if IsHeadless() {
		return nil, ErrHeadless
	}

	pkce, err := GeneratePKCE()
	if err != nil {
		return nil, err
	}
	state, err := generateState()
	if err != nil {
		return nil, err
	}

	ln, port, err := bindLoopback()
	if err != nil {
		return nil, err
	}
	defer ln.Close()

	redirectURI := fmt.Sprintf("http://localhost:%d/auth/callback", port)
	authURL := buildAuthorizeURL(m.opts, redirectURI, pkce, state)

	fmt.Fprintf(status, "Opening browser for ChatGPT sign-in...\nIf it does not open, visit:\n%s\n", authURL)
	_ = openBrowser(authURL)

	type cbResult struct {
		code string
		err  error
	}
	resCh := make(chan cbResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != state {
			http.Error(w, "State mismatch", http.StatusBadRequest)
			return
		}
		if e := q.Get("error"); e != "" {
			desc := q.Get("error_description")
			fmt.Fprintf(w, "Sign-in failed: %s %s. You can close this tab.", e, desc)
			resCh <- cbResult{err: fmt.Errorf("codexauth: oauth error: %s %s", e, desc)}
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "Missing authorization code", http.StatusBadRequest)
			resCh <- cbResult{err: errors.New("codexauth: callback missing authorization code")}
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<html><body><h2>SuperCli sign-in complete</h2><p>You can close this tab and return to the terminal.</p></body></html>")
		resCh <- cbResult{code: code}
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	var code string
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("codexauth: login cancelled: %w", ctx.Err())
	case r := <-resCh:
		if r.err != nil {
			return nil, r.err
		}
		code = r.code
	}

	tokens, err := m.exchangeCode(ctx, code, redirectURI, pkce.CodeVerifier)
	if err != nil {
		return nil, err
	}
	af := &AuthFile{Tokens: tokens, LastRefresh: time.Now()}
	if err := Save(m.path, af); err != nil {
		return nil, err
	}
	return &LoginResult{
		AccountID: tokens.AccountID,
		PlanType:  ParsePlanType(tokens.AccessToken),
	}, nil
}

// exchangeCode swaps the authorization code for tokens at
// <issuer>/oauth/token (form-encoded, like the reference).
func (m *Manager) exchangeCode(ctx context.Context, code, redirectURI, verifier string) (*TokenData, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {m.opts.ClientID},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		trimSlash(m.opts.Issuer)+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := m.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("codexauth: token exchange: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("codexauth: token exchange failed (http %d): %s", resp.StatusCode, string(body))
	}
	var tok struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("codexauth: token exchange parse: %w", err)
	}
	return &TokenData{
		IDToken:      tok.IDToken,
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		AccountID:    ParseAccountID(tok.IDToken),
	}, nil
}

// buildAuthorizeURL constructs the browser URL with the same
// query parameters as the Codex CLI reference.
func buildAuthorizeURL(opts Options, redirectURI string, pkce PKCECodes, state string) string {
	q := url.Values{
		"response_type":              {"code"},
		"client_id":                  {opts.ClientID},
		"redirect_uri":               {redirectURI},
		"scope":                      {"openid profile email offline_access"},
		"code_challenge":             {pkce.CodeChallenge},
		"code_challenge_method":      {"S256"},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
		"state":                      {state},
		"originator":                 {"codex_cli_go"},
	}
	return trimSlash(opts.Issuer) + "/oauth/authorize?" + q.Encode()
}

// bindLoopback binds 127.0.0.1:1455, falling back to 1457.
func bindLoopback() (net.Listener, int, error) {
	for _, port := range []int{defaultCallbackPort, fallbackCallbackPort} {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			return ln, port, nil
		}
	}
	return nil, 0, fmt.Errorf("codexauth: callback ports %d and %d are in use — close other login windows and retry",
		defaultCallbackPort, fallbackCallbackPort)
}

// IsHeadless reports whether the environment cannot host an
// interactive browser login.
func IsHeadless() bool {
	if os.Getenv("SUPERCLI_HEADLESS") == "1" {
		return true
	}
	if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return true
	}
	return false
}

// openBrowser launches the system browser. Failure is non-fatal:
// the auth URL is always printed for manual use.
func openBrowser(u string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	case "darwin":
		return exec.Command("open", u).Start()
	default:
		return exec.Command("xdg-open", u).Start()
	}
}
