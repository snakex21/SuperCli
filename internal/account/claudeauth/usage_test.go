package claudeauth

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClaudeUsagePercentagesUnknownAndExpiredWindows(t *testing.T) {
	now := time.Now().UTC()
	s, err := parseUsage([]byte(fmt.Sprintf(`{"five_hour":{"utilization":28.5,"resets_at":%q},"seven_day":{"utilization":null,"resets_at":null},"seven_day_opus":null}`, now.Add(time.Hour).Format(time.RFC3339))), now)
	if err != nil || len(s.Windows) != 1 || *s.Windows[0].RemainingPercent != 71.5 {
		t.Fatalf("usage: %+v / %v", s, err)
	}
	expired := s.At(now.Add(2 * time.Hour))
	if !expired.Windows[0].Stale || *expired.Windows[0].UsedPercent != 28.5 {
		t.Fatal("expiry invented unused capacity")
	}
	if s.Windows[0].Stale {
		t.Fatal("At mutated original snapshot")
	}
	if _, err := parseUsage([]byte(`{"five_hour":{"utilization":999}}`), now); err == nil {
		t.Fatal("invalid percentage accepted")
	}
}

func TestClaudeUsageRefreshBindsAccountAndKeepsSafeCacheOnFailure(t *testing.T) {
	rejected := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access-secret" || r.Header.Get("x-api-key") != "" {
			t.Error("invalid usage authentication")
		}
		if rejected {
			http.Error(w, "access-secret", http.StatusTooManyRequests)
			return
		}
		fmt.Fprintf(w, `{"five_hour":{"utilization":25,"resets_at":%q},"seven_day":{"utilization":40,"resets_at":%q}}`, time.Now().Add(time.Hour).Format(time.RFC3339), time.Now().Add(24*time.Hour).Format(time.RFC3339))
	}))
	defer server.Close()
	dir := t.TempDir()
	m := NewManager(dir, Options{UsageURL: server.URL})
	if err := m.save(Tokens{AccessToken: "access-secret", RefreshToken: "refresh-secret", LoginID: "first-login", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	s, err := m.RefreshUsage(context.Background())
	if err != nil || len(s.Windows) != 2 || *s.Windows[0].RemainingPercent != 75 {
		t.Fatalf("refresh: %+v / %v", s, err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "claude-usage.json"))
	if err != nil || strings.Contains(string(b), "access-secret") || strings.Contains(string(b), "refresh-secret") {
		t.Fatal("usage cache leaked credentials")
	}
	rejected = true
	if _, err := m.RefreshUsage(context.Background()); err == nil || strings.Contains(err.Error(), "access-secret") {
		t.Fatalf("unsafe error: %v", err)
	}
	if m.Usage() == nil {
		t.Fatal("failed refresh destroyed saved snapshot")
	}
	if err := m.save(Tokens{AccessToken: "new-account", LoginID: "second-login", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if m.Usage() != nil {
		t.Fatal("quota from another account reused")
	}
}

func TestClaudeResponseHeadersUpdateWithoutNetworkAndIgnoreOldLogin(t *testing.T) {
	m := NewManager(t.TempDir(), Options{})
	if err := m.save(Tokens{AccessToken: "current", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-utilization", "0.28")
	headers.Set("anthropic-ratelimit-unified-5h-reset", fmt.Sprint(time.Now().Add(time.Hour).Unix()))
	m.ObserveUsageHeaders(headers, "old-account")
	if m.Usage() != nil {
		t.Fatal("old login response contaminated current account")
	}
	m.ObserveUsageHeaders(headers, "current")
	s := m.Usage()
	if s == nil || len(s.Windows) != 1 || math.Abs(*s.Windows[0].UsedPercent-28) > 1e-9 || math.Abs(*s.Windows[0].RemainingPercent-72) > 1e-9 {
		t.Fatalf("fraction header not converted to percent: %+v", s)
	}
	if err := m.Logout(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(m.path), "claude-usage.json")); !os.IsNotExist(err) {
		t.Fatal("logout left account usage cached")
	}
}
