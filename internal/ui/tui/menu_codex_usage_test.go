package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"supercli/internal/account/codexauth"
	"supercli/internal/llm"
	"supercli/internal/llm/providers"
)

func usageTestPtr[T any](value T) *T { return &value }

func codexUsageTestModel(t *testing.T) Model {
	t.Helper()
	m := New(Options{Home: t.TempDir(), DataDir: t.TempDir(), Language: "en"})
	m.width, m.height = 120, 36
	m.enterMenu(interactiveMenu{kind: menuUsage, category: 2})
	return m
}

func TestCodexUsageWeeklyOnlyUnknownAndObservedZero(t *testing.T) {
	m := codexUsageTestModel(t)
	now := time.Now()
	usage := &llm.CodexUsageSnapshot{CapturedAt: &now, PlanType: "pro", RateLimits: []llm.CodexUsageLimit{{ID: "codex", Secondary: &llm.CodexUsageWindow{WindowSeconds: usageTestPtr(int64(604800))}}}}
	m.menu.usage = &usageSnapshot{codexAccounts: []llm.CodexAccountUsage{{Label: "personal", LoggedIn: true, Usage: usage}}, codexSummary: llm.CodexUsageSummary{Accounts: 1, Unknown: 1}}
	rows := m.codexUsageRows()
	if len(rows) != 3 || !strings.Contains(rows[2].label, "7 days window") || rows[2].badge != "—" {
		t.Fatalf("weekly-only rows = %#v", rows)
	}
	if !strings.Contains(rows[2].meta, "Used —") || !strings.Contains(rows[2].meta, "Resets: —") {
		t.Fatalf("unknown observation rendered as known: %q", rows[2].meta)
	}
	view := m.renderUsageMenu()
	if strings.Contains(view, "5 h") || strings.Contains(view, `\n`) || !strings.Contains(view, "pro") {
		t.Fatalf("weekly view = %q", view)
	}
	usage.RateLimits[0].Secondary.UsedPercent = usageTestPtr(0.0)
	usage.RateLimits[0].Secondary.RemainingPercent = usageTestPtr(100.0)
	rows = m.codexUsageRows()
	if rows[2].badge != "0%" || !strings.Contains(rows[2].meta, "Remaining 100%") {
		t.Fatalf("observed zero was lost: %#v", rows[2])
	}
}

func TestCodexUsageAccountStatesAndTotalsAreSeparate(t *testing.T) {
	m := codexUsageTestModel(t)
	now := time.Now()
	credit := "0"
	available := &llm.CodexUsageSnapshot{CapturedAt: &now, Credits: &llm.CodexUsageCredits{Balance: &credit}, RateLimits: []llm.CodexUsageLimit{{ID: "codex", Allowed: usageTestPtr(true), Secondary: &llm.CodexUsageWindow{WindowSeconds: usageTestPtr(int64(604800)), UsedPercent: usageTestPtr(100.0)}}, {ID: "model-only", Allowed: usageTestPtr(false)}}}
	unknown := &llm.CodexUsageSnapshot{CapturedAt: &now, RateLimits: []llm.CodexUsageLimit{{ID: "model-only", Allowed: usageTestPtr(false)}}}
	if codexUsageAccountState(available, now) != "available" || codexUsageAccountState(unknown, now) != "unknown" {
		t.Fatal("model bucket or usage percentage overrode general server availability")
	}
	m.menu.usage = &usageSnapshot{codexAccounts: []llm.CodexAccountUsage{{Label: "personal", LoggedIn: true, Usage: available}, {Label: "work", LoggedIn: true}, {Label: "logged-out"}}, codexSummary: llm.CodexUsageSummary{Accounts: 2, Available: 1, Unknown: 1}}
	rows := m.codexUsageRows()
	if rows[0].badge != "2" || !strings.Contains(rows[0].meta, "Available: 1") || strings.Contains(rows[0].meta, "%") {
		t.Fatalf("aggregate must count accounts, not add usage: %#v", rows[0])
	}
	var all []string
	for _, row := range rows {
		all = append(all, row.label, row.badge, row.meta, strings.Join(row.detail, "\n"))
	}
	joined := strings.Join(all, "\n")
	if strings.Contains(joined, "logged-out") || !strings.Contains(joined, "Credits: 0") || !strings.Contains(joined, "Unknown") {
		t.Fatalf("account presentation lost observations: %q", joined)
	}
}

func TestCodexUsageAsyncRefreshKeepsCacheAndNavigation(t *testing.T) {
	m := codexUsageTestModel(t)
	old := &usageSnapshot{codexAccounts: []llm.CodexAccountUsage{{Label: "cached", LoggedIn: true}}, codexSummary: llm.CodexUsageSummary{Accounts: 1, Unknown: 1}}
	m.menu.usage = old
	calls := 0
	load := func(_ context.Context, dataDir, label string, all, refresh bool, opts codexauth.Options) ([]llm.CodexAccountUsage, llm.CodexUsageSummary, error) {
		calls++
		if dataDir != m.dataDir || label != "cached" || all || !refresh || opts.BackendURL != "" {
			t.Fatal("manual per-account request changed contract")
		}
		return nil, llm.CodexUsageSummary{}, errors.New("usage refresh failed")
	}
	next, cmd := m.beginCodexUsageWith(true, "cached", load)
	pending := next.(Model)
	if calls != 0 || cmd == nil || !pending.menu.usage.codexLoading || pending.menu.usage.codexAccounts[0].Label != "cached" {
		t.Fatal("refresh must defer I/O and preserve cached rows")
	}
	if _, repeated := pending.handleUsageKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}); repeated != nil {
		t.Fatal("refresh already in flight should not queue another request")
	}
	msg := cmd().(usageLoadedMsg)
	if calls != 1 || msg.err != nil || msg.data.codexLoading || msg.data.codexError == "" || msg.data.codexAccounts[0].Label != "cached" {
		t.Fatal("failed refresh discarded cached data or stayed loading")
	}
	applied, _ := pending.Update(msg)
	if !strings.Contains(applied.(Model).renderUsageMenu(), "usage refresh failed") {
		t.Fatal("refresh error should remain visible with stale data")
	}
	closed, _ := pending.handleUsageKey(tea.KeyMsg{Type: tea.KeyEsc})
	after, _ := closed.(Model).Update(msg)
	if after.(Model).menu.kind == menuUsage {
		t.Fatal("late refresh reopened closed usage")
	}
	newRequest, _ := pending.beginCodexUsageWith(false, "", load)
	newer := newRequest.(Model)
	updated, _ := newer.Update(msg)
	if updated.(Model).menu.usage != newer.menu.usage {
		t.Fatal("late old refresh overwrote newer request")
	}
}

func TestCodexUsageEntryIsCacheOnlyAndTabsStayReachable(t *testing.T) {
	m := codexUsageTestModel(t)
	called := false
	load := func(_ context.Context, _ string, label string, all, refresh bool, _ codexauth.Options) ([]llm.CodexAccountUsage, llm.CodexUsageSummary, error) {
		called = true
		if refresh || !all || label != "" {
			t.Fatal("opening limits must not refresh provider usage")
		}
		return []llm.CodexAccountUsage{}, llm.CodexUsageSummary{}, nil
	}
	pending, cmd := m.beginCodexUsageWith(false, "", load)
	if called || cmd == nil || len(pending.(Model).codexUsageRows()) != 0 {
		t.Fatal("initial cache load must not imply observed zero before completion")
	}
	loaded := cmd().(usageLoadedMsg)
	result, _ := pending.(Model).Update(loaded)
	if !called || !strings.Contains(result.(Model).renderUsageMenu(), m.tr("acct.usage.noAccounts")) {
		t.Fatal("empty account cache should be explicit")
	}
	for _, hasSession := range []bool{false, true} {
		tab := m
		if hasSession {
			tab.loadedSessionID = "saved"
		}
		right, command := tab.handleUsageKey(tea.KeyMsg{Type: tea.KeyRight})
		if right.(Model).menu.category != 0 || command == nil {
			t.Fatal("Codex tab cannot return to normal runtime usage")
		}
		back, command := right.(Model).handleUsageKey(tea.KeyMsg{Type: tea.KeyLeft})
		if back.(Model).menu.category != 2 || command == nil {
			t.Fatal("Codex cached tab is not reachable")
		}
	}
}

func TestCodexUsageUsesConfiguredAuthOptions(t *testing.T) {
	m := codexUsageTestModel(t)
	m.providerMgr = providers.NewManager(m.dataDir)
	want := codexauth.Options{BackendURL: "https://fixture.invalid/codex", ClientID: "fixture-client"}
	m.providerMgr.SetCodexAuthOptions(want)
	load := func(_ context.Context, _ string, _ string, _ bool, _ bool, got codexauth.Options) ([]llm.CodexAccountUsage, llm.CodexUsageSummary, error) {
		if got.BackendURL != want.BackendURL || got.ClientID != want.ClientID {
			t.Fatal("usage lost the configured backend/auth options")
		}
		return nil, llm.CodexUsageSummary{}, nil
	}
	_, cmd := m.beginCodexUsageWith(false, "", load)
	cmd()
}

func TestAccountsMenuUsesRealNewlinesAndUsageHint(t *testing.T) {
	m := codexUsageTestModel(t)
	m.enterMenu(interactiveMenu{kind: menuAccounts})
	view := m.renderAccountsMenu()
	if strings.Contains(view, `\n`) || !strings.Contains(view, "\n") || !strings.Contains(view, "Enter account limits") {
		t.Fatalf("account navigation hint or line breaks are wrong: %q", view)
	}
}

func TestAccountsDeleteKeyUsesSelectedAccountRemoval(t *testing.T) {
	m := New(Options{DataDir: t.TempDir(), Language: "en"})
	for _, label := range []string{"one", "two"} {
		if err := codexauth.Save(codexauth.AuthFilePathFor(m.dataDir, label), &codexauth.AuthFile{Tokens: &codexauth.TokenData{AccessToken: "synthetic"}}); err != nil {
			t.Fatal(err)
		}
	}
	m.enterMenu(interactiveMenu{kind: menuAccounts, cursor: 1})
	var removed string
	m.commands = map[string]SlashHandler{"logout": func(_ context.Context, args string) (string, error) {
		removed = args
		return "removed", codexauth.NewManagerFor(m.dataDir, args, codexauth.Options{}).Logout()
	}}
	next, cmd, handled := m.menuAccountsKey("delete")
	if !handled || cmd == nil {
		t.Fatal("Del did not dispatch removal")
	}
	msg := cmd()
	out, _ := next.(Model).Update(msg)
	got := out.(Model)
	if removed != "two" || !got.codexAccountsChanged {
		t.Fatal("wrong account removed or pool not invalidated")
	}
	if codexauth.NewManagerFor(m.dataDir, "two", codexauth.Options{}).LoggedIn() || !codexauth.NewManagerFor(m.dataDir, "one", codexauth.Options{}).LoggedIn() {
		t.Fatal("removal did not preserve other account")
	}
	m.menu.cursor = 1 // add action after removal
	if _, _, handled = m.menuAccountsKey("delete"); handled {
		t.Fatal("Del removed add-account action")
	}
}
