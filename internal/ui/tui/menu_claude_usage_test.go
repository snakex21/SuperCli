package tui

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"supercli/internal/account/claudeauth"
)

func TestClaudeUsageTabLoadsRemainingSubscriptionLocally(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	tokens, _ := json.Marshal(claudeauth.Tokens{AccessToken: "fixture", LoginID: "test-login", ExpiresAt: now.Add(time.Hour)})
	if err := os.WriteFile(filepath.Join(dir, "claude-auth.json"), tokens, 0600); err != nil {
		t.Fatal(err)
	}
	used := 28.0
	reset := now.Add(time.Hour)
	digest := sha256.Sum256([]byte("test-login"))
	cache, _ := json.Marshal(map[string]any{"identity": fmt.Sprintf("%x", digest[:]), "usage": claudeauth.UsageSnapshot{CapturedAt: now, Windows: []claudeauth.UsageWindow{{ID: "five_hour", WindowSeconds: 18000, UsedPercent: &used, ResetsAt: &reset, CapturedAt: now}}}})
	if err := os.WriteFile(filepath.Join(dir, "claude-usage.json"), cache, 0600); err != nil {
		t.Fatal(err)
	}
	m := New(Options{Home: t.TempDir(), Language: "pl", NoColor: true})
	m.dataDir = dir
	m.enterMenu(interactiveMenu{kind: menuUsage, category: 2})
	next, cmd := m.handleUsageKey(tea.KeyMsg{Type: tea.KeyRight})
	if cmd == nil || next.(Model).menu.category != 3 {
		t.Fatal("Claude usage tab not reachable from ChatGPT")
	}
	loaded, _ := next.(Model).Update(cmd())
	m = loaded.(Model)
	rows := m.claudeUsageItems()
	if len(rows) != 1 || !strings.Contains(rows[0].badge, "72%") || !strings.Contains(m.subscriptionRemainingLabel(), "Claude 72%") {
		t.Fatalf("remaining subscription not displayed: %+v", rows)
	}
	m.menu.usage.claudeUsage.Windows[0].CapturedAt = now.Add(-time.Hour)
	if rows = m.claudeUsageItems(); !strings.Contains(rows[0].badge, "72%") || !strings.Contains(rows[0].meta, m.tr("acct.usage.stale")) {
		t.Fatal("old but unexpired quota hidden or not marked stale")
	}
	expired := now.Add(-time.Minute)
	m.menu.usage.claudeUsage.Windows[0].ResetsAt = &expired
	if rows = m.claudeUsageItems(); !strings.Contains(rows[0].badge, "—") || strings.Contains(rows[0].badge, "72%") {
		t.Fatal("expired quota presented as current")
	}
}
