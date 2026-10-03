package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/account/codexauth"
)

func scalarCodexLimits(rl CodexRateLimits) CodexRateLimits { rl.Snapshot = nil; return rl }

func TestCodexUsageSnapshotPercentMissingInvalidAndOverQuota(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		body  string
		known bool
		pct   float64
	}{
		{`{"rate_limit":{"primary_window":{}}}`, false, 0},
		{`{"rate_limit":{"primary_window":{"used_percent":null}}}`, false, 0},
		{`{"rate_limit":{"primary_window":{"used_percent":-1}}}`, false, 0},
		{`{"rate_limit":{"primary_window":{"used_percent":107.25}}}`, true, 107.25},
	} {
		s, ok := parseCodexUsageSnapshot([]byte(tc.body), now)
		if !ok {
			t.Fatal("recognized window rejected")
		}
		w := s.RateLimits[0].Primary
		if (w.UsedPercent != nil) != tc.known {
			t.Fatal("missing or invalid percentage fabricated")
		}
		if tc.known && (*w.UsedPercent != tc.pct || *w.RemainingPercent != 0 || !codexLimitsFromSnapshot(s).ExhaustedAt(now)) {
			t.Fatal("server over-quota percentage discarded")
		}
	}
}

func TestCodexUsageSnapshotServerWindowsAndUnknownValues(t *testing.T) {
	now := time.Unix(1800000000, 0)
	body := []byte(`{"plan_type":"pro","rate_limit":{"allowed":true,"primary_window":null,"secondary_window":{"used_percent":25.5,"limit_window_seconds":604800,"reset_after_seconds":120}},"additional_rate_limits":[{"limit_name":"Code review","metered_feature":"review","rate_limit":{"allowed":false,"limit_reached":true,"primary_window":{"limit_window_seconds":90,"reset_at":1800000300}}}],"credits":{"has_credits":true,"unlimited":false,"balance":"12.50"}}`)
	s, ok := parseCodexUsageSnapshot(body, now)
	if !ok || len(s.RateLimits) != 2 || s.RateLimits[0].Primary != nil {
		t.Fatalf("server windows lost: %+v", s)
	}
	w := s.RateLimits[0].Secondary
	if *w.WindowSeconds != 604800 || *w.ResetsAt != now.Unix()+120 || *w.UsedPercent != 25.5 || *w.RemainingPercent != 74.5 {
		t.Fatalf("window fields changed: %+v", w)
	}
	if extra := s.RateLimits[1]; extra.ID != "review" || extra.Primary.UsedPercent != nil || extra.Primary.RemainingPercent != nil || *extra.Primary.WindowSeconds != 90 {
		t.Fatalf("unknown additional percentage fabricated: %+v", extra)
	}
	if s.PlanType != "pro" || s.Credits == nil || *s.Credits.Balance != "12.50" || s.Availability != "available" {
		t.Fatal("plan/credit/general availability lost")
	}
	rl := codexLimitsFromSnapshot(s)
	if text := rl.formatDetailAt(now); strings.Contains(text, "5h") || !strings.Contains(text, "7d") || !strings.Contains(text, "90s") || !strings.Contains(text, "25.5%") {
		t.Fatalf("duration/percentage guessed: %s", text)
	}
	encoded, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"used_percent":null`) || !strings.Contains(string(encoded), `"primary":null`) {
		t.Fatal("unknown values did not remain null")
	}
}

func TestCodexUsageSnapshotFreshnessAndAuthoritativeAvailability(t *testing.T) {
	now := time.Unix(1800000000, 0)
	s := CodexUsageSnapshot{CapturedAt: codexPtr(now), RateLimits: []CodexUsageLimit{{ID: "codex", Allowed: codexPtr(false), Primary: &CodexUsageWindow{UsedPercent: codexPtr(100.0), ResetsAt: codexPtr(now.Unix() + 60)}}}}
	rl := codexLimitsFromSnapshot(s)
	if !rl.ExhaustedAt(now) {
		t.Fatal("fresh denied account not exhausted")
	}
	stale := s.At(now.Add(time.Minute))
	if !stale.Stale || stale.Availability != "unknown" || *stale.RateLimits[0].Primary.UsedPercent != 100 || rl.ExhaustedAt(now.Add(time.Minute)) {
		t.Fatal("expired quota was fabricated or excluded")
	}
	s.RateLimits[0].Allowed = codexPtr(true)
	if known, exhausted := s.AvailabilityAt(now); !known || exhausted {
		t.Fatal("explicit allowed entitlement ignored")
	}
	s.RateLimits[0].Allowed = nil
	if known, exhausted := s.AvailabilityAt(now); !known || !exhausted {
		t.Fatal("observed 100% quota ignored")
	}
	s.RateLimits[0].Primary.ResetsAt = nil
	if known, _ := s.AvailabilityAt(now.Add(16 * time.Minute)); known {
		t.Fatal("aged observation remained authoritative")
	}
	s.CapturedAt = codexPtr(now.Add(time.Hour))
	if known, _ := s.AvailabilityAt(now); known {
		t.Fatal("future cached observation remained authoritative")
	}
}

func TestCodexUsageSnapshotsOwnAllMutableFields(t *testing.T) {
	now := time.Now()
	s := CodexUsageSnapshot{CapturedAt: &now, Credits: &CodexUsageCredits{HasCredits: codexPtr(true), Balance: codexPtr("5")}, RateLimits: []CodexUsageLimit{{ID: "codex", Allowed: codexPtr(true), Primary: &CodexUsageWindow{UsedPercent: codexPtr(30.0), RemainingPercent: codexPtr(70.0), WindowSeconds: codexPtr(int64(77)), ResetsAt: codexPtr(now.Unix() + 100)}}}}
	p := &CodexProvider{}
	p.setRateLimits("account", codexLimitsFromSnapshot(s))
	*s.RateLimits[0].Primary.UsedPercent = 99
	first, _ := p.UsageSnapshot()
	if *first.RateLimits[0].Primary.UsedPercent != 30 {
		t.Fatal("setter borrowed snapshot")
	}
	*first.RateLimits[0].Allowed = false
	*first.RateLimits[0].Primary.UsedPercent = 80
	*first.Credits.Balance = "other"
	*first.CapturedAt = time.Time{}
	second, _ := p.UsageSnapshot()
	if *second.RateLimits[0].Primary.UsedPercent != 30 || !*second.RateLimits[0].Allowed || *second.Credits.Balance != "5" || second.CapturedAt.IsZero() {
		t.Fatal("getter exposed mutable provider data")
	}
}

func TestCodexHeaderRefreshPreservesAdditionalCaptureAgeAndIdentity(t *testing.T) {
	now := time.Now()
	old := now.Add(-time.Hour)
	s := CodexUsageSnapshot{CapturedAt: &old, PlanType: "pro", Credits: &CodexUsageCredits{CapturedAt: &old, Balance: codexPtr("5")}, RateLimits: []CodexUsageLimit{{ID: "codex", Allowed: codexPtr(false)}, {ID: "review", CapturedAt: &old, Primary: &CodexUsageWindow{UsedPercent: codexPtr(100.0)}}}}
	p := &CodexProvider{cfg: CodexConfig{AccountID: "account"}}
	p.setRateLimits("account", codexLimitsFromSnapshot(s))
	p.setRateLimits("account", parseCodexRateLimits(hdr(map[string]string{"X-Codex-Primary-Used-Percent": "10", "X-Codex-Primary-Window-Minutes": "17"})))
	got, _ := p.UsageSnapshot()
	if got.PlanType != "pro" || got.Credits == nil || len(got.RateLimits) != 2 || !got.RateLimits[1].Primary.Stale || got.Availability != "available" {
		t.Fatal("headers refreshed old extra limits or lost account metadata")
	}
	p.setRateLimits("other", codexLimitsFromSnapshot(CodexUsageSnapshot{CapturedAt: &now, PlanType: "other"}))
	got, _ = p.UsageSnapshot()
	if got.PlanType != "pro" {
		t.Fatal("different account polluted provider")
	}
}

func TestCodexUsageScopedCacheCollisionAndClear(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	for _, id := range []string{"A", "a", "a/b", "a?b"} {
		s := CodexUsageSnapshot{CapturedAt: &now, PlanType: id, RateLimits: []CodexUsageLimit{{ID: "codex", Allowed: codexPtr(true)}}}
		if err := saveCodexRateLimits(dir, id, codexLimitsFromSnapshot(s)); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"A", "a", "a/b", "a?b"} {
		s, ok := LoadCodexUsageSnapshot(dir, id)
		if !ok || s.PlanType != id {
			t.Fatal("cache identity collision")
		}
	}
	if err := ClearCodexAccountRateLimits(dir, "a/b"); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadCodexUsageSnapshot(dir, "a/b"); ok {
		t.Fatal("logged-out account cache retained")
	}
	for _, id := range []string{"A", "a", "a?b"} {
		if _, ok := LoadCodexUsageSnapshot(dir, id); !ok {
			t.Fatal("logout removed another account")
		}
	}
	wrong, _ := json.Marshal(codexRateLimitsSnapshot{SavedAt: now, Limits: CodexRateLimits{OK: true}})
	if err := os.WriteFile(codexRateLimitsPath(dir, "unknown-owner"), wrong, 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadCodexUsageSnapshot(dir, "unknown-owner"); ok {
		t.Fatal("unscoped envelope matched authenticated account")
	}
}

func TestCodexUsageLegacyScopedMigrationAndScopedClear(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	legacy := codexRateLimitsSnapshot{SavedAt: now, AccountID: "acct", Limits: CodexRateLimits{PrimaryUsedPct: 25, PrimaryWindowMin: 13, OK: true}}
	data, _ := json.Marshal(legacy)
	path := filepath.Join(dir, "codex_ratelimits-acct.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	s, ok := LoadCodexUsageSnapshot(dir, "acct")
	if !ok || *s.RateLimits[0].Primary.WindowSeconds != 780 {
		t.Fatal("matching old scoped cache lost")
	}
	if err := ClearCodexAccountRateLimits(dir, "acct"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("matching legacy cache retained")
	}
}

func seedCodexUsageAccount(t *testing.T, dir, label, id string) {
	t.Helper()
	if err := codexauth.Save(codexauth.AuthFilePathFor(dir, label), &codexauth.AuthFile{LastRefresh: time.Now(), Tokens: &codexauth.TokenData{AccessToken: "synthetic-access", AccountID: id}}); err != nil {
		t.Fatal(err)
	}
}

func TestCodexManualUsageAccountsAndCachedReads(t *testing.T) {
	dir := t.TempDir()
	seedCodexUsageAccount(t, dir, "one", "acct-one")
	seedCodexUsageAccount(t, dir, "two", "acct-two")
	seedCodexUsageAccount(t, dir, "alias", "acct-one")
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/api/codex/usage" || r.Header.Get("Authorization") != "Bearer synthetic-access" {
			t.Error("incorrect manual usage endpoint")
		}
		if r.Header.Get("chatgpt-account-id") == "acct-one" {
			_, _ = w.Write([]byte(`{"plan_type":"pro","rate_limit":{"allowed":true,"primary_window":null,"secondary_window":{"used_percent":10,"limit_window_seconds":604800}},"credits":{"unlimited":true}}`))
		} else {
			_, _ = w.Write([]byte(`{"plan_type":"plus","rate_limit":{"allowed":false,"primary_window":{"used_percent":100,"limit_window_seconds":7200,"reset_after_seconds":120}}}`))
		}
	}))
	defer server.Close()
	opts := codexauth.Options{BackendURL: server.URL}
	before, summary, err := ListCodexAccountUsageWithOptions(dir, opts)
	if err != nil || hits.Load() != 0 || summary.Accounts != 2 || summary.Unknown != 2 || len(before) != 3 {
		t.Fatal("cached account read sent request or combined aliases")
	}
	accounts, summary, err := RefreshCodexAccountUsageWithOptions(context.Background(), dir, "", true, opts)
	if err != nil || hits.Load() != 2 || summary.Available != 1 || summary.Exhausted != 1 || summary.Unknown != 0 {
		t.Fatalf("manual per-account refresh: %v %+v hits=%d", err, summary, hits.Load())
	}
	cached, after, err := ListCodexAccountUsageWithOptions(dir, opts)
	if err != nil || hits.Load() != 2 || after != summary || len(cached) != len(accounts) {
		t.Fatal("cached view refetched usage")
	}
	for _, a := range cached {
		if a.Usage == nil || a.Usage.PlanType != a.PlanType {
			t.Fatal("account plan/snapshot lost")
		}
	}
}

func TestCodexUsageFailureDoesNotExposeBodiesOrDiscardOtherAccounts(t *testing.T) {
	dir := t.TempDir()
	seedCodexUsageAccount(t, dir, "one", "acct-one")
	seedCodexUsageAccount(t, dir, "two", "acct-two")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("chatgpt-account-id") == "acct-one" {
			_, _ = w.Write([]byte(`{"rate_limit":{"allowed":true}}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("SENSITIVE-UPSTREAM-BODY"))
	}))
	defer server.Close()
	accounts, summary, err := RefreshCodexAccountUsageWithOptions(context.Background(), dir, "", true, codexauth.Options{BackendURL: server.URL})
	if err == nil || summary.Available != 1 || summary.Unknown != 1 {
		t.Fatal("partial failure discarded successful account")
	}
	body, _ := json.Marshal(accounts)
	if strings.Contains(string(body), "SENSITIVE") || strings.Contains(err.Error(), "SENSITIVE") {
		t.Fatal("upstream error body exposed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := RefreshCodexAccountUsageWithOptions(ctx, dir, "", true, codexauth.Options{BackendURL: server.URL}); err == nil {
		t.Fatal("cancelled manual refresh succeeded")
	}
}

func TestCodexUsageFetchRejectsOversizeWithoutReplacingSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(strings.Repeat(" ", 256*1024+1))) }))
	defer server.Close()
	p, err := NewCodex(CodexConfig{BackendURL: server.URL, Model: "synthetic", Tokens: &fakeTokens{access: "synthetic", accountID: "acct"}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	p.setRateLimits("acct", codexLimitsFromSnapshot(CodexUsageSnapshot{CapturedAt: &now, PlanType: "pro", RateLimits: []CodexUsageLimit{{ID: "codex", Allowed: codexPtr(true)}}}))
	before, _ := p.UsageSnapshot()
	if _, err := p.FetchUsage(context.Background()); err == nil {
		t.Fatal("oversize usage body accepted")
	}
	after, _ := p.UsageSnapshot()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed usage refresh replaced snapshot")
	}
}

type codexUsageChangingTokens struct{ id string }

func (t *codexUsageChangingTokens) Token(context.Context) (string, string, error) {
	return "synthetic-access", t.id, nil
}
func (t *codexUsageChangingTokens) Refresh(context.Context) (string, error) {
	t.id = "other-account"
	return "synthetic-refreshed", nil
}

func TestCodexUsageIdentityChangeAfterUnauthorizedDoesNotRetryWrongAccount(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(http.StatusUnauthorized) }))
	defer server.Close()
	tokens := &codexUsageChangingTokens{id: "account"}
	p, err := NewCodex(CodexConfig{BackendURL: server.URL, Model: "synthetic", Tokens: tokens, AccountID: "account", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.FetchUsage(context.Background()); err == nil || hits.Load() != 1 {
		t.Fatal("changed identity retried under original account")
	}
	if _, ok := p.RateLimits(); ok {
		t.Fatal("changed identity stored quota")
	}
}

func TestCodexUsageLogoutDuringManualRefreshDoesNotRepublishAccount(t *testing.T) {
	dir := t.TempDir()
	seedCodexUsageAccount(t, dir, "one", "acct-one")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := codexauth.NewManagerFor(dir, "one", codexauth.Options{}).Logout(); err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte(`{"rate_limit":{"allowed":true}}`))
	}))
	defer server.Close()
	accounts, summary, err := RefreshCodexAccountUsageWithOptions(context.Background(), dir, "one", false, codexauth.Options{BackendURL: server.URL})
	if err == nil || summary.Accounts != 0 {
		t.Fatal("logout race republished authenticated account")
	}
	for _, account := range accounts {
		if account.LoggedIn {
			t.Fatal("logout race returned stale login metadata")
		}
	}
	if _, ok := LoadCodexUsageSnapshot(dir, "acct-one"); ok {
		t.Fatal("logout race republished cache")
	}
}
