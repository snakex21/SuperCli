package llm

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/account/claudeauth"
	"supercli/internal/account/codexauth"
)

func writeClaudeLogin(t *testing.T, dir string, usage *claudeauth.UsageSnapshot) {
	t.Helper()
	tokens, _ := json.Marshal(claudeauth.Tokens{AccessToken: "fixture", LoginID: "refresh-login", ExpiresAt: time.Now().Add(time.Hour)})
	if err := os.WriteFile(filepath.Join(dir, "claude-auth.json"), tokens, 0600); err != nil {
		t.Fatal(err)
	}
	if usage == nil {
		return
	}
	digest := sha256.Sum256([]byte("refresh-login"))
	b, _ := json.Marshal(map[string]any{"identity": fmt.Sprintf("%x", digest[:]), "usage": usage})
	if err := os.WriteFile(filepath.Join(dir, "claude-usage.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSubscriptionRefreshSkipsLoggedOutAccounts(t *testing.T) {
	var calls atomic.Int32
	r := &SubscriptionUsageRefresher{
		Claude: func(context.Context, string) error { calls.Add(1); return nil },
		Codex:  func(context.Context, string, string, codexauth.Options) error { calls.Add(1); return nil },
	}
	if refreshed, busy := r.RefreshDue(context.Background(), t.TempDir(), codexauth.Options{}, "claude"); refreshed || busy || calls.Load() != 0 {
		t.Fatalf("logged-out accounts requested usage: refreshed=%v busy=%v calls=%d", refreshed, busy, calls.Load())
	}
}

func TestSubscriptionRefreshMissingSnapshotThrottledAndForced(t *testing.T) {
	dir := t.TempDir()
	writeClaudeLogin(t, dir, nil)
	now := time.Now()
	var calls atomic.Int32
	r := &SubscriptionUsageRefresher{Now: func() time.Time { return now }, Claude: func(context.Context, string) error { calls.Add(1); return fmt.Errorf("offline") }}
	if refreshed, _ := r.RefreshDue(context.Background(), dir, codexauth.Options{}, ""); refreshed || calls.Load() != 1 {
		t.Fatalf("missing snapshot not requested once (failure must be silent): calls=%d", calls.Load())
	}
	now = now.Add(30 * time.Second)
	r.RefreshDue(context.Background(), dir, codexauth.Options{}, "claude")
	if calls.Load() != 1 {
		t.Fatal("refresh not throttled within 60s, even when forced")
	}
	now = now.Add(31 * time.Second)
	r.RefreshDue(context.Background(), dir, codexauth.Options{}, "")
	if calls.Load() != 2 {
		t.Fatal("refresh not retried after throttle window")
	}
}

func TestSubscriptionRefreshFreshSnapshotOnlyWhenForcedOrOld(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	used, reset := 20.0, now.Add(3*time.Hour)
	writeClaudeLogin(t, dir, &claudeauth.UsageSnapshot{CapturedAt: now, Windows: []claudeauth.UsageWindow{{ID: "five_hour", WindowSeconds: 18000, UsedPercent: &used, ResetsAt: &reset, CapturedAt: now}}})
	var calls atomic.Int32
	r := &SubscriptionUsageRefresher{Now: func() time.Time { return now }, Claude: func(context.Context, string) error { calls.Add(1); return nil }}
	r.RefreshDue(context.Background(), dir, codexauth.Options{}, "")
	if calls.Load() != 0 {
		t.Fatal("fresh snapshot refreshed without need")
	}
	r.RefreshDue(context.Background(), dir, codexauth.Options{}, "codex")
	if calls.Load() != 0 {
		t.Fatal("other provider's run forced a Claude refresh")
	}
	if refreshed, _ := r.RefreshDue(context.Background(), dir, codexauth.Options{}, "claude"); !refreshed || calls.Load() != 1 {
		t.Fatal("finished Claude run did not refresh")
	}
	now = now.Add(SubscriptionRefreshMaxAge + time.Second)
	r.RefreshDue(context.Background(), dir, codexauth.Options{}, "")
	if calls.Load() != 2 {
		t.Fatal("periodic refresh of an old snapshot missing")
	}
}

func TestSubscriptionRefreshSingleFlight(t *testing.T) {
	dir := t.TempDir()
	writeClaudeLogin(t, dir, nil)
	release, entered := make(chan struct{}), make(chan struct{})
	r := &SubscriptionUsageRefresher{Claude: func(context.Context, string) error { close(entered); <-release; return nil }}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); r.RefreshDue(context.Background(), dir, codexauth.Options{}, "") }()
	<-entered
	if _, busy := r.RefreshDue(context.Background(), dir, codexauth.Options{}, ""); !busy {
		t.Fatal("concurrent refresh not single-flight")
	}
	close(release)
	wg.Wait()
}
