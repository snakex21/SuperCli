package webgui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeAccountStatusAndLogoutNeverExposeCredentials(t *testing.T) {
	srv := newTestServer(t, false)
	file := filepath.Join(srv.eng.dataDir, "claude-auth.json")
	if err := os.WriteFile(file, []byte(`{"access_token":"access-secret","refresh_token":"refresh-secret"}`), 0600); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localProviderRequest(http.MethodGet, "/api/claude/account"))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"logged_in":true`) || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("status leaked credentials: %d / %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Cache-Control"), "no-store") {
		t.Fatal("account status is cacheable")
	}
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localProviderJSONRequest(http.MethodPost, "/api/claude/logout", `{}`))
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("logout did not remove credentials: %v", err)
	}
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localProviderRequest(http.MethodGet, "/api/claude/account"))
	if !strings.Contains(rec.Body.String(), `"logged_in":false`) {
		t.Fatal("logout status not updated")
	}
}

func TestClaudeAuthEndpointsRejectWrongMethodsAndRemoteAccess(t *testing.T) {
	srv := newTestServer(t, true)
	for _, route := range []string{"/api/claude/account", "/api/claude/login", "/api/claude/logout", "/api/claude/usage"} {
		req := localProviderJSONRequest(http.MethodPost, route, `{}`)
		if route != "/api/claude/account" {
			req.Method = http.MethodGet
		}
		req.Header.Set("Authorization", "Bearer "+srv.sessionToken)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("wrong method %s: %d", route, rec.Code)
		}
		req = localProviderJSONRequest(http.MethodPost, route, `{}`)
		req.RemoteAddr = "192.0.2.123:43210"
		req.Header.Set("Authorization", "Bearer "+srv.sessionToken)
		rec = httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("remote %s: %d", route, rec.Code)
		}
	}
}
