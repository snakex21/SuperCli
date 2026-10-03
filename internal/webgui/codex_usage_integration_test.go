package webgui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/account/codexauth"
	"supercli/internal/llm"
	"supercli/internal/system/config"
)

func TestCodexUsageCachedManualAndScopedLogout(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/api/codex/usage" || r.Header.Get("Authorization") != "Bearer synthetic-access" {
			t.Error("usage used wrong configured endpoint")
		}
		if r.Header.Get("chatgpt-account-id") == "synthetic-one" {
			_, _ = w.Write([]byte(`{"plan_type":"pro","rate_limit":{"allowed":true,"primary_window":null,"secondary_window":{"used_percent":23.5,"limit_window_seconds":604800}},"credits":{"balance":"7.25","has_credits":true}}`))
		} else {
			_, _ = w.Write([]byte(`{"plan_type":"plus","rate_limit":{"allowed":false,"primary_window":{"used_percent":100,"limit_window_seconds":7200,"reset_after_seconds":3600}}}`))
		}
	}))
	defer upstream.Close()
	srv := newTestServer(t, false)
	if err := config.SaveToml(filepath.Join(srv.eng.dataDir, "config.toml"), config.TomlConfig{CodexAuth: config.CodexAuthConf{BackendURL: upstream.URL}}); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"one", "two"} {
		if err := codexauth.Save(codexauth.AuthFilePathFor(srv.eng.dataDir, label), &codexauth.AuthFile{LastRefresh: time.Now(), Tokens: &codexauth.TokenData{AccessToken: "synthetic-access", AccountID: "synthetic-" + label}}); err != nil {
			t.Fatal(err)
		}
	}
	read := func() struct {
		Accounts []codexAccountView    `json:"accounts"`
		Summary  llm.CodexUsageSummary `json:"usage_summary"`
	} {
		t.Helper()
		rec := httptest.NewRecorder()
		srv.handleCodexAccounts(rec, httptest.NewRequest(http.MethodGet, "/api/codex/accounts", nil))
		var out struct {
			Accounts []codexAccountView    `json:"accounts"`
			Summary  llm.CodexUsageSummary `json:"usage_summary"`
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("cached accounts returned %d", rec.Code)
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := read(); hits.Load() != 0 || out.Summary.Unknown != 2 {
		t.Fatal("cached GET fetched usage")
	}
	rec := httptest.NewRecorder()
	srv.handleCodexUsage(rec, httptest.NewRequest(http.MethodPost, "/api/codex/usage", strings.NewReader(`{"all":true}`)))
	if rec.Code != http.StatusOK || hits.Load() != 2 {
		t.Fatalf("manual refresh failed: %d hits=%d", rec.Code, hits.Load())
	}
	out := read()
	if hits.Load() != 2 || out.Summary.Available != 1 || out.Summary.Exhausted != 1 {
		t.Fatal("manual/cached account summary incorrect")
	}
	if out.Accounts[0].Usage == nil || out.Accounts[0].Usage.RateLimits[0].Primary != nil || out.Accounts[0].Usage.PlanType != "pro" {
		t.Fatal("Pro dashboard fabricated primary window")
	}
	rec = httptest.NewRecorder()
	srv.handleCodexLogout(rec, httptest.NewRequest(http.MethodPost, "/api/codex/logout", strings.NewReader(`{"label":"one"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("logout returned %d", rec.Code)
	}
	if _, ok := llm.LoadCodexUsageSnapshot(srv.eng.dataDir, "synthetic-one"); ok {
		t.Fatal("logged-out usage retained")
	}
	if _, ok := llm.LoadCodexUsageSnapshot(srv.eng.dataDir, "synthetic-two"); !ok {
		t.Fatal("logout removed other account usage")
	}
	if out = read(); out.Summary.Accounts != 1 || out.Summary.Exhausted != 1 || hits.Load() != 2 {
		t.Fatal("logout/GET made unrelated requests or counted removed account")
	}
}

func TestCodexUsageInvalidRequestIsReadOnly(t *testing.T) {
	srv := newTestServer(t, false)
	for _, tc := range []struct {
		method, body string
		want         int
	}{{http.MethodGet, `{}`, http.StatusMethodNotAllowed}, {http.MethodPost, `{broken`, http.StatusBadRequest}} {
		rec := httptest.NewRecorder()
		srv.handleCodexUsage(rec, httptest.NewRequest(tc.method, "/api/codex/usage", strings.NewReader(tc.body)))
		if rec.Code != tc.want {
			t.Fatalf("invalid manual request returned %d want %d", rec.Code, tc.want)
		}
	}
}
