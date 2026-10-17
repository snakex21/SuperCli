package tui

import "testing"

func TestSettingsDarwinPoolSize(t *testing.T) {
	m := newSettingsModel(t, "")
	m = cursorForKey(m, "darwin_pool_size")
	if value, source := m.settingValueSource(settingRow{key: "darwin_pool_size"}, m.menu.settingsCfg); value != m.tr("tui.setting_darwin_pool_size.auto") || source != "default" {
		t.Fatalf("default = %q/%q", value, source)
	}
	commit := func(buf string) {
		next, _ := m.settingsEnter()
		m = next.(Model)
		if !m.menu.editing {
			t.Fatal("int row did not enter edit mode")
		}
		m.menu.editBuf = buf
		next, _ = m.settingsCommitInt()
		m = next.(Model)
	}
	commit("4")
	if got := loadCfg(t, m).DarwinPoolSize; got != 4 {
		t.Fatalf("saved %d, want 4", got)
	}
	if value, source := m.settingValueSource(settingRow{key: "darwin_pool_size"}, m.menu.settingsCfg); value != "4" || source != "manual" {
		t.Fatalf("display = %q/%q", value, source)
	}
	commit("25")
	if got := loadCfg(t, m).DarwinPoolSize; got != 10 {
		t.Fatalf("saved %d, want clamp to 10", got)
	}
	commit("0")
	if got := loadCfg(t, m).DarwinPoolSize; got != 0 {
		t.Fatalf("saved %d, want 0 (auto)", got)
	}
	commit("3")
	m = cursorForKey(m, "")
	next, _ := m.settingsEnter()
	m = next.(Model)
	if got := loadCfg(t, m).DarwinPoolSize; got != 0 {
		t.Fatalf("reset all kept darwin_pool_size=%d", got)
	}
}
