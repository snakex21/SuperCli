package webgui

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/account/claudeauth"
	"supercli/internal/account/codexauth"
	"supercli/internal/llm"
)

func TestSubscriptionUsageKeepsChatGPTAndClaudeSeparateWithoutFetching(t *testing.T) {
	srv := newTestServer(t, false)
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, `{"rate_limit":{"allowed":true,"primary_window":{"used_percent":40,"limit_window_seconds":18000,"reset_after_seconds":3600},"secondary_window":{"used_percent":10,"limit_window_seconds":604800,"reset_after_seconds":86400}}}`)
	}))
	defer upstream.Close()
	opts := codexauth.Options{BackendURL: upstream.URL}
	srv.eng.providerManager().SetCodexAuthOptions(opts)
	if err := codexauth.Save(codexauth.AuthFilePath(srv.eng.dataDir), &codexauth.AuthFile{Tokens: &codexauth.TokenData{AccessToken: "chatgpt-secret", AccountID: "chatgpt-fixture"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := llm.RefreshCodexAccountUsageWithOptions(context.Background(), srv.eng.dataDir, "", true, opts); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tokens, _ := json.Marshal(claudeauth.Tokens{AccessToken: "claude-secret", LoginID: "fixture-login", ExpiresAt: now.Add(time.Hour)})
	if err := os.WriteFile(filepath.Join(srv.eng.dataDir, "claude-auth.json"), tokens, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("fixture-login"))
	used := 28.0
	reset := now.Add(time.Hour)
	usage, _ := json.Marshal(map[string]any{"identity": fmt.Sprintf("%x", digest[:]), "usage": claudeauth.UsageSnapshot{CapturedAt: now, Windows: []claudeauth.UsageWindow{{ID: "five_hour", WindowSeconds: 18000, UsedPercent: &used, ResetsAt: &reset, CapturedAt: now}}}})
	if err := os.WriteFile(filepath.Join(srv.eng.dataDir, "claude-usage.json"), usage, 0600); err != nil {
		t.Fatal(err)
	}
	views := srv.eng.subscriptionUsage()
	if len(views) != 2 || views[0].Provider != "ChatGPT" || views[1].Provider != "Claude" {
		t.Fatalf("accounts missing: %+v", views)
	}
	if len(views[0].Windows) != 2 || *views[0].Windows[0].RemainingPercent != 60 || *views[1].Windows[0].RemainingPercent != 72 {
		t.Fatalf("quota windows combined or lost: %+v", views)
	}
	stats, err := srv.eng.stats(context.Background(), "")
	if err != nil || len(stats.SubscriptionUsage) != 2 {
		t.Fatalf("stats omitted subscriptions: %v", err)
	}
	costs, err := srv.eng.costs(context.Background(), "")
	if err != nil || len(costs.SubscriptionUsage) != 2 {
		t.Fatalf("cost view omitted subscriptions: %v", err)
	}
	b, _ := json.Marshal(stats.SubscriptionUsage)
	if strings.Contains(string(b), "secret") {
		t.Fatal("statistics leaked credentials")
	}
	if requests.Load() != 1 {
		t.Fatalf("cached reads fetched usage: %d", requests.Load())
	}
	if err := claudeauth.NewManager(srv.eng.dataDir, claudeauth.Options{}).Logout(); err != nil {
		t.Fatal(err)
	}
	if remaining := srv.eng.subscriptionUsage(); len(remaining) != 1 || remaining[0].Provider != "ChatGPT" {
		t.Fatal("Claude logout changed the ChatGPT account")
	}
}
