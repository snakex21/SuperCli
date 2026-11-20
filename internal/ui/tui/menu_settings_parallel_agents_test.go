package tui

import (
	"strings"
	"testing"
)

// 1.0.37: "Max. parallel agents" in /settings: a number, 0 / "bez limitu"
// = unlimited, empty = default 6; per-provider caps as provider=number.
func TestSettings_MaxParallelAgents(t *testing.T) {
	m := newSettingsModel(t, "")
	m = cursorForKey(m, "max_parallel_agents")
	if v, src := m.settingValueSource(m.localizedSettingsRows()[m.menu.cursor], m.menu.settingsCfg); src != "default" || !strings.Contains(v, "6") {
		t.Fatalf("default display = %q %q", v, src)
	}
	commit := func(in string) Model {
		mm, _ := m.settingsEnter()
		m = mm.(Model)
		m.menu.editBuf = in
		mm, _ = m.settingsCommitText()
		return mm.(Model)
	}
	m = commit("bez limitu")
	if m.menu.formErr != "" {
		t.Fatalf("rejected: %s", m.menu.formErr)
	}
	if c := loadCfg(t, m); c.MaxParallelAgents == nil || *c.MaxParallelAgents != 0 {
		t.Fatalf("persisted = %v", c.MaxParallelAgents)
	}
	if v, src := m.settingValueSource(m.localizedSettingsRows()[m.menu.cursor], m.menu.settingsCfg); src != "manual" || v == "" || strings.Contains(v, "6") {
		t.Fatalf("unlimited display = %q %q", v, src)
	}
	m = commit("12")
	if c := loadCfg(t, m); c.MaxParallelAgents == nil || *c.MaxParallelAgents != 12 {
		t.Fatalf("12 persisted as %v", c.MaxParallelAgents)
	}
	m = commit("-3")
	if m.menu.formErr == "" || *loadCfg(t, m).MaxParallelAgents != 12 {
		t.Fatal("negative value must be rejected without a change")
	}
	m.menu.formErr = ""
	mm, _ := m.settingsResetCurrent()
	m = mm.(Model)
	if loadCfg(t, m).MaxParallelAgents != nil {
		t.Fatal("reset must restore the default")
	}

	m = cursorForKey(m, "max_parallel_agents_per_provider")
	m = commit("lmstudio=1; openrouter=4")
	if c := loadCfg(t, m); c.MaxParallelAgentsPerProvider["lmstudio"] != 1 || c.MaxParallelAgentsPerProvider["openrouter"] != 4 {
		t.Fatalf("per provider = %v (err %q)", c.MaxParallelAgentsPerProvider, m.menu.formErr)
	}
}
