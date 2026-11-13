package claudeauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoginPKCEPortableRefreshAndLogout(t *testing.T) {
	var challenge, state, redirect string
	var refreshes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Error("wrong token request")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["client_id"] != "test-client" {
			t.Error("wrong client ID")
		}
		if body["grant_type"] == "authorization_code" {
			verifier, _ := body["code_verifier"].(string)
			sum := sha256.Sum256([]byte(verifier))
			if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge || len(verifier) < 43 {
				t.Error("PKCE not bound to exchange")
			}
			if body["code"] != "test-code" || body["state"] != state || body["redirect_uri"] != redirect {
				t.Error("callback not bound to exchange")
			}
			fmt.Fprint(w, `{"access_token":"initial-secret","refresh_token":"refresh-secret","expires_in":3600}`)
		} else {
			refreshes.Add(1)
			if body["refresh_token"] != "refresh-secret" {
				t.Error("wrong refresh token")
			}
			// Omitted refresh token must preserve the previous token.
			fmt.Fprint(w, `{"access_token":"renewed-secret","expires_in":3600}`)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	opts := Options{ClientID: "test-client", TokenURL: server.URL}
	mgr := NewManager(dir, opts)
	if mgr.LoggedIn() {
		t.Fatal("new manager is logged in")
	}
	err := mgr.Login(context.Background(), func(raw string) error {
		u, err := url.Parse(raw)
		if err != nil {
			return err
		}
		q := u.Query()
		challenge, state, redirect = q.Get("code_challenge"), q.Get("state"), q.Get("redirect_uri")
		if q.Get("code_challenge_method") != "S256" || !strings.Contains(q.Get("scope"), "user:inference") || len(state) < 32 {
			t.Error("invalid authorization parameters")
		}
		// A forged callback cannot consume the real callback slot.
		resp, err := http.Get(redirect + "?code=forged&state=wrong")
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Error("accepted wrong OAuth state")
		}
		resp, err = http.Get(redirect + "?code=test-code&state=" + url.QueryEscape(state))
		if err != nil {
			return err
		}
		resp.Body.Close()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !mgr.LoggedIn() {
		t.Fatal("login was not saved")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "claude-auth.json" {
		t.Fatalf("unexpected portable files: %v, %v", entries, err)
	}
	// Copying the app data folder carries its login with it.
	moved := t.TempDir()
	b, _ := os.ReadFile(filepath.Join(dir, "claude-auth.json"))
	if err := os.WriteFile(filepath.Join(moved, "claude-auth.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if token, err := NewManager(moved, opts).AccessToken(context.Background(), false); err != nil || token != "initial-secret" {
		t.Fatalf("portable login: %q / %v", token, err)
	}
	mgr.state.mu.Lock()
	tokens, _ := mgr.load()
	tokens.ExpiresAt = time.Now().Add(-time.Second)
	if err := mgr.save(tokens); err != nil {
		t.Fatal(err)
	}
	mgr.state.mu.Unlock()
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := NewManager(dir, opts).AccessToken(context.Background(), false)
			if err != nil || token != "renewed-secret" {
				t.Errorf("refresh: %q / %v", token, err)
			}
		}()
	}
	wg.Wait()
	if refreshes.Load() != 1 {
		t.Fatalf("concurrent refreshes: %d", refreshes.Load())
	}
	if err := NewManager(dir, opts).Logout(); err != nil {
		t.Fatal(err)
	}
	if mgr.LoggedIn() {
		t.Fatal("logout invisible to existing manager")
	}
	if _, err := mgr.AccessToken(context.Background(), false); err == nil {
		t.Fatal("logged-out manager still supplies a token")
	}
	if err := mgr.Logout(); err != nil {
		t.Fatal(err)
	}
}

func TestLoginCancellationAndDeniedCallback(t *testing.T) {
	for _, denied := range []bool{false, true} {
		mgr := NewManager(t.TempDir(), Options{})
		ctx, cancel := context.WithCancel(context.Background())
		err := mgr.Login(ctx, func(raw string) error {
			if !denied {
				cancel()
				return nil
			}
			u, _ := url.Parse(raw)
			q := u.Query()
			resp, err := http.Get(q.Get("redirect_uri") + "?error=access_denied&state=" + url.QueryEscape(q.Get("state")))
			if err == nil {
				resp.Body.Close()
			}
			return err
		})
		cancel()
		if err == nil || mgr.LoggedIn() {
			t.Fatalf("denied=%v: %v", denied, err)
		}
	}
}

func TestTokenErrorsDoNotLeakSecretsOrOverwriteSavedLogin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "refresh-secret", http.StatusUnauthorized)
	}))
	defer server.Close()
	mgr := NewManager(t.TempDir(), Options{TokenURL: server.URL})
	if err := mgr.save(Tokens{AccessToken: "saved-secret", RefreshToken: "refresh-secret", ExpiresAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	_, err := mgr.AccessToken(context.Background(), false)
	if err == nil || strings.Contains(err.Error(), "refresh-secret") {
		t.Fatalf("unsafe error: %v", err)
	}
	tokens, err := mgr.load()
	if err != nil || tokens.AccessToken != "saved-secret" {
		t.Fatal("failed refresh destroyed previous credentials")
	}
}

func TestEmptyDataDirectoryIsRejected(t *testing.T) {
	mgr := NewManager("", Options{})
	if mgr.LoggedIn() {
		t.Fatal("empty data dir is authenticated")
	}
	if err := mgr.Logout(); err == nil {
		t.Fatal("empty data dir accepted")
	}
	if err := mgr.Login(context.Background(), func(string) error { t.Fatal("opened browser without portable data dir"); return nil }); err == nil {
		t.Fatal("empty data dir accepted")
	}
}
