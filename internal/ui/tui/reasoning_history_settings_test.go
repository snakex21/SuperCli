package tui

import (
	"strings"
	"supercli/internal/llm"
	"testing"
)

func TestSettingsReasoningHistoryToggleAndReset(t *testing.T) {
	old := llm.DiscardPreviousReasoning()
	t.Cleanup(func() { llm.SetDiscardPreviousReasoning(old) })
	llm.SetDiscardPreviousReasoning(false)
	m := cursorForKey(newSettingsModel(t, ""), "discard_previous_reasoning")
	if !strings.Contains(m.renderSettingsMenu(), "Usuwaj wcześniejsze myślenie") {
		t.Fatal("setting not rendered")
	}
	next, _ := m.settingsEnter()
	m = next.(Model)
	cfg := loadCfg(t, m)
	if cfg.DiscardPreviousReasoning == nil || !*cfg.DiscardPreviousReasoning || !llm.DiscardPreviousReasoning() {
		t.Fatal("toggle not saved/applied")
	}
	next, _ = m.settingsResetCurrent()
	m = next.(Model)
	if loadCfg(t, m).DiscardPreviousReasoning != nil || llm.DiscardPreviousReasoning() {
		t.Fatal("reset did not restore keep default")
	}
}
