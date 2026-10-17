package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/system/config"
)

func TestSettingsPostReviewSharedConfigRoundtrip(t *testing.T) {
	m := newSettingsModel(t, "review_model = \"off\"\ntask_model = \"local/worker\"\ncontext_window = 32768\nprune_protect_tokens = 9000\n")
	if !config.ReviewDisabled(loadCfg(t, m)) {
		t.Fatal("off should disable review")
	}
	m = cursorForKey(m, "review_model")
	next, _ := m.settingsEnter()
	m = next.(Model)
	if m.menu.editBuf != "off" {
		t.Fatalf("review raw = %q", m.menu.editBuf)
	}
	m.menu.editBuf = "local/checker"
	next, _ = m.settingsEditKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	got := loadCfg(t, m)
	if got.ReviewModel != "local/checker" || config.ReviewDisabled(got) || got.TaskModel != "local/worker" || got.ContextWindow != 32768 || got.PruneProtectTokens != 9000 {
		t.Fatalf("shared config changed: %+v", got)
	}
	// An external GUI-style write must be read fresh by the TUI editor.
	got.ReviewModel = "cloud/new-checker"
	if err := config.SaveToml(m.settingsGlobalPath(), got); err != nil {
		t.Fatal(err)
	}
	next, _ = m.openSettingsMenu()
	m = cursorForKey(next.(Model), "review_model")
	next, _ = m.settingsEnter()
	m = next.(Model)
	if m.menu.editBuf != got.ReviewModel {
		t.Fatalf("stale review model: %q", m.menu.editBuf)
	}
	m.menu.editBuf = ""
	next, _ = m.settingsEditKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if !config.ReviewDisabled(loadCfg(t, m)) {
		t.Fatal("empty should disable review")
	}
}
