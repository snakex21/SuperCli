package tui

// audit-130: /settings typed the way a user does it, value change with keys,
// persistence in an isolated temp config, and Esc cancelling an edit.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"supercli/internal/system/config"
)

func audit130SettingsModel(t *testing.T, lang string) (Model, string) {
	t.Helper()
	restoreRetryGlobals(t)
	dir := t.TempDir()
	m := New(Options{Home: dir, DataDir: dir, Language: lang})
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	return out.(Model), dir
}

func audit130OpenSettingsByTyping(t *testing.T, m Model) Model {
	t.Helper()
	m = audit130Type(m, "/settings")
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = out.(Model)
	if m.mode != modeMenu || m.menu.kind != menuSettings {
		t.Fatalf("typing /settings + Enter did not open settings: mode=%d kind=%v input=%q", m.mode, m.menu.kind, m.input.Value())
	}
	return m
}

func audit130Keys(m Model, keys ...tea.KeyMsg) Model {
	for _, k := range keys {
		out, _ := m.Update(k)
		m = out.(Model)
	}
	return m
}

var (
	audit130Enter = tea.KeyMsg{Type: tea.KeyEnter}
	audit130Esc   = tea.KeyMsg{Type: tea.KeyEsc}
	audit130Down  = tea.KeyMsg{Type: tea.KeyDown}
)

func audit130Runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// moveToSetting walks the real key path: Tab across categories, Down rows.
func audit130MoveToSetting(t *testing.T, m Model, key string) Model {
	t.Helper()
	for c := 0; c < len(m.settingsCategories()); c++ {
		rows := m.localizedSettingsRows()
		for i := range rows {
			if rows[i].key == key {
				for m.menu.cursor < i {
					m = audit130Keys(m, audit130Down)
				}
				if got := m.localizedSettingsRows()[m.menu.cursor].key; got != key {
					t.Fatalf("cursor on %q, want %q", got, key)
				}
				return m
			}
		}
		m = audit130Keys(m, tea.KeyMsg{Type: tea.KeyTab})
	}
	t.Fatalf("setting %q not reachable with Tab/Down", key)
	return m
}

func TestAudit130SettingsEditPersistsAndEscCancels(t *testing.T) {
	m, dir := audit130SettingsModel(t, "pl")
	m = audit130OpenSettingsByTyping(t, m)
	if view := ansi.Strip(m.View()); !strings.Contains(view, textFor("pl", "tui.menu_navigation.74a883a037")) && !strings.Contains(strings.ToLower(view), "ustawienia") {
		t.Fatalf("settings panel not rendered:\n%s", view)
	}
	m = audit130MoveToSetting(t, m, "max_retries")

	// Enter -> type 7 -> Enter persists.
	m = audit130Keys(m, audit130Enter, audit130Runes("7"), audit130Enter)
	if m.menu.formErr != "" {
		t.Fatalf("edit rejected: %s", m.menu.formErr)
	}
	cfg, err := config.LoadToml(filepath.Join(dir, "config.toml"))
	if err != nil || cfg.MaxRetriesSetting() != 7 {
		t.Fatalf("max_retries not persisted to temp config: %v %+v", err, cfg.MaxRetries)
	}

	// Enter -> type 9 -> Esc: edit cancelled, value stays 7, panel stays.
	m = audit130Keys(m, audit130Enter, audit130Runes("9"), audit130Esc)
	if m.menu.editing || m.mode != modeMenu {
		t.Fatalf("Esc in edit closed the panel or kept editing: mode=%d editing=%v", m.mode, m.menu.editing)
	}
	if cfg, _ := config.LoadToml(filepath.Join(dir, "config.toml")); cfg.MaxRetriesSetting() != 7 {
		t.Fatalf("Esc changed the value to %d", cfg.MaxRetriesSetting())
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "7") {
		t.Fatalf("panel does not show persisted 7:\n%s", view)
	}

	// Esc closes settings and gives the composer focus back.
	m = audit130Keys(m, audit130Esc)
	if m.mode != modeNormal || !m.input.Focused() {
		t.Fatalf("Esc did not close settings: mode=%d focused=%v", m.mode, m.input.Focused())
	}
	m = audit130Type(m, "q")
	if m.input.Value() != "q" {
		t.Fatalf("composer after settings = %q", m.input.Value())
	}
	// The /settings text itself must not be left in the composer.
	if strings.Contains(m.input.Value(), "settings") {
		t.Fatalf("slash text left behind: %q", m.input.Value())
	}
}

func TestAudit130SettingsToggleWithEnterPersists(t *testing.T) {
	m, dir := audit130SettingsModel(t, "en")
	m = audit130OpenSettingsByTyping(t, m)
	m = audit130MoveToSetting(t, m, "update_notify")
	before, _ := config.LoadToml(filepath.Join(dir, "config.toml"))
	m = audit130Keys(m, audit130Enter)
	if m.menu.formErr != "" {
		t.Fatalf("toggle error: %s", m.menu.formErr)
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatalf("toggle did not write the temp config: %v", err)
	}
	after, _ := config.LoadToml(filepath.Join(dir, "config.toml"))
	if string(mustJSON(before.UpdateNotify)) == string(mustJSON(after.UpdateNotify)) {
		t.Fatalf("update_notify unchanged after Enter; config:\n%s", data)
	}
	// Esc out leaves the file as saved (no rollback, no extra write).
	m = audit130Keys(m, audit130Esc)
	again, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	if string(again) != string(data) {
		t.Fatalf("closing settings rewrote the config")
	}
}

// Invalid input must keep the old value and show an error in the panel.
func TestAudit130SettingsInvalidValueShowsError(t *testing.T) {
	m, dir := audit130SettingsModel(t, "pl")
	m = audit130OpenSettingsByTyping(t, m)
	m = audit130MoveToSetting(t, m, "max_retries")
	m = audit130Keys(m, audit130Enter, audit130Runes("4"), audit130Enter)
	m = audit130Keys(m, audit130Enter, audit130Runes("abc"), audit130Enter)
	if cfg, _ := config.LoadToml(filepath.Join(dir, "config.toml")); cfg.MaxRetriesSetting() != 4 {
		t.Fatalf("invalid edit changed value to %d", cfg.MaxRetriesSetting())
	}
	if m.menu.formErr == "" {
		t.Fatalf("no error shown for invalid input; editing=%v buf=%q", m.menu.editing, m.menu.editBuf)
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, firstLine(m.menu.formErr)) {
		t.Fatalf("error %q not visible:\n%s", m.menu.formErr, view)
	}
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	if len([]rune(s)) > 20 {
		s = string([]rune(s)[:20])
	}
	return s
}

func mustJSON(v *bool) string {
	if v == nil {
		return "nil"
	}
	if *v {
		return "true"
	}
	return "false"
}
