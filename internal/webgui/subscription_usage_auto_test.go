package webgui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/account/claudeauth"
)

func TestSubscriptionUsageAutoEndpointRefreshesSilentlyAndThrottles(t *testing.T) {
	srv := newTestServer(t, false)
	tokens, _ := json.Marshal(claudeauth.Tokens{AccessToken: "claude-secret", LoginID: "auto-login", ExpiresAt: time.Now().Add(time.Hour)})
	if err := os.WriteFile(filepath.Join(srv.eng.dataDir, "claude-auth.json"), tokens, 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	srv.eng.subUsage.Claude = func(context.Context, string) error { calls.Add(1); return nil }
	post := func(method, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, localProviderJSONRequest(method, "/api/subscription/usage/auto", body))
		return rec
	}
	rec := post(http.MethodPost, `{}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"refreshed":true`) || calls.Load() != 1 {
		t.Fatalf("missing snapshot not refreshed: %d %s calls=%d", rec.Code, rec.Body.String(), calls.Load())
	}
	rec = post(http.MethodPost, `{"force":true}`)
	if !strings.Contains(rec.Body.String(), `"refreshed":false`) || calls.Load() != 1 {
		t.Fatalf("second request within 60s not throttled: %s calls=%d", rec.Body.String(), calls.Load())
	}
	if rec = post(http.MethodGet, ``); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET allowed: %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatal("credentials leaked")
	}
}
