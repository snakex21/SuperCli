package codexauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"runtime"
	"testing"
	"time"
)

func TestGeneratePKCE(t *testing.T) {
	p, err := GeneratePKCE()
	if err != nil {
		t.Fatal(err)
	}
	if l := len(p.CodeVerifier); l < 43 || l > 128 {
		t.Fatalf("verifier length %d out of RFC 7636 range [43,128]", l)
	}
	sum := sha256.Sum256([]byte(p.CodeVerifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if p.CodeChallenge != want {
		t.Fatalf("challenge mismatch: got %q want %q", p.CodeChallenge, want)
	}
	p2, _ := GeneratePKCE()
	if p2.CodeVerifier == p.CodeVerifier {
		t.Fatal("two PKCE verifiers should not collide")
	}
}

func fakeJWT(t *testing.T, accountID string) string {
	t.Helper()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	header := enc(map[string]string{"alg": "none", "typ": "JWT"})
	payload := enc(map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
			"chatgpt_plan_type":  "plus",
		},
	})
	return header + "." + payload + "." + base64.RawURLEncoding.EncodeToString([]byte("sig"))
}

func TestParseAccountIDAndPlan(t *testing.T) {
	jwt := fakeJWT(t, "acc-123")
	if got := ParseAccountID(jwt); got != "acc-123" {
		t.Fatalf("ParseAccountID = %q, want acc-123", got)
	}
	if got := ParsePlanType(jwt); got != "plus" {
		t.Fatalf("ParsePlanType = %q, want plus", got)
	}
	if got := ParseAccountID("not-a-jwt"); got != "" {
		t.Fatalf("garbage JWT should yield empty account id, got %q", got)
	}
}

func TestAuthFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := AuthFilePath(dir)
	af := &AuthFile{
		Tokens: &TokenData{
			IDToken:      "id",
			AccessToken:  "access",
			RefreshToken: "refresh",
			AccountID:    "acc",
		},
		LastRefresh: time.Now().UTC().Truncate(time.Second),
	}
	if err := Save(path, af); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Tokens == nil || got.Tokens.AccessToken != "access" ||
		got.Tokens.RefreshToken != "refresh" || got.Tokens.AccountID != "acc" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	// Permissions: only meaningful on Unix.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("auth.json perm = %o, want 0600", perm)
		}
	}
	// Clear removes the file; second clear is a no-op.
	if err := Clear(path); err != nil {
		t.Fatal(err)
	}
	if err := Clear(path); err != nil {
		t.Fatal(err)
	}
	if got, err := Load(path); err != nil || got != nil {
		t.Fatalf("Load after Clear = (%v, %v), want (nil, nil)", got, err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "nope", "auth.json"))
	if err != nil || got != nil {
		t.Fatalf("missing file should be (nil, nil), got (%v, %v)", got, err)
	}
}

func TestOptionsWithDefaults(t *testing.T) {
	o := Options{}.WithDefaults()
	if o.ClientID != DefaultClientID || o.Issuer != DefaultIssuer || o.BackendURL != DefaultBackendURL {
		t.Fatalf("defaults not applied: %+v", o)
	}
	o = Options{ClientID: "x", Issuer: "https://i", BackendURL: "https://b"}.WithDefaults()
	if o.ClientID != "x" || o.Issuer != "https://i" || o.BackendURL != "https://b" {
		t.Fatalf("overrides lost: %+v", o)
	}
}

func TestManagerRefresh(t *testing.T) {
	newJWT := fakeJWT(t, "acc-new")
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"id_token":      newJWT,
			"access_token":  "new-access",
			"refresh_token": "new-refresh",
		})
	}))
	defer srv.Close()

	home := t.TempDir()
	m := NewManager(home, Options{Issuer: srv.URL, ClientID: "client-1"})
	if m.LoggedIn() {
		t.Fatal("fresh manager should not be logged in")
	}
	seed := &AuthFile{
		Tokens: &TokenData{
			AccessToken:  "old-access",
			RefreshToken: "old-refresh",
			AccountID:    "acc-old",
		},
		LastRefresh: time.Now().Add(-40 * 24 * time.Hour), // stale → refresh on Token()
	}
	if err := Save(m.Path(), seed); err != nil {
		t.Fatal(err)
	}
	if !m.LoggedIn() {
		t.Fatal("manager should be logged in after Save")
	}

	access, accountID, err := m.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if access != "new-access" || accountID != "acc-new" {
		t.Fatalf("Token() = (%q, %q), want (new-access, acc-new)", access, accountID)
	}
	if gotBody["grant_type"] != "refresh_token" || gotBody["refresh_token"] != "old-refresh" || gotBody["client_id"] != "client-1" {
		t.Fatalf("refresh request body wrong: %v", gotBody)
	}

	// Refreshed tokens must be persisted.
	af, err := Load(m.Path())
	if err != nil {
		t.Fatal(err)
	}
	if af.Tokens.RefreshToken != "new-refresh" || af.Tokens.AccessToken != "new-access" {
		t.Fatalf("refresh not persisted: %+v", af.Tokens)
	}

	// Fresh tokens are not refreshed again: kill the server and
	// confirm Token() still succeeds from disk.
	srv.Close()
	access, _, err = m.Token(context.Background())
	if err != nil || access != "new-access" {
		t.Fatalf("fresh token should be served from disk: (%q, %v)", access, err)
	}
}

func TestManagerRefreshFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"refresh_token_expired"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	home := t.TempDir()
	m := NewManager(home, Options{Issuer: srv.URL})
	_ = Save(m.Path(), &AuthFile{
		Tokens:      &TokenData{AccessToken: "a", RefreshToken: "r"},
		LastRefresh: time.Now(),
	})
	if _, err := m.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh should fail on 401 from token endpoint")
	}
}

func TestTokenNotLoggedIn(t *testing.T) {
	m := NewManager(t.TempDir(), Options{})
	if _, _, err := m.Token(context.Background()); err == nil {
		t.Fatal("Token without auth.json should error")
	}
}

func TestBuildAuthorizeURL(t *testing.T) {
	pkce := PKCECodes{CodeVerifier: "v", CodeChallenge: "c"}
	u := buildAuthorizeURL(Options{}.WithDefaults(), "http://localhost:1455/auth/callback", pkce, "st")
	for _, want := range []string{
		DefaultIssuer + "/oauth/authorize?",
		"client_id=" + DefaultClientID,
		"code_challenge=c",
		"code_challenge_method=S256",
		"state=st",
		"response_type=code",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("authorize URL missing %q:\n%s", want, u)
		}
	}
}
