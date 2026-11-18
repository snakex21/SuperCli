package tui

import (
	"testing"

	"supercli/internal/system/config"
	"supercli/internal/system/uilang"
)

// The memory switch is a normal /settings row in every UI language, cycles
// default(on) -> on -> off -> default and is cleared by "reset all".
func TestSettingsMemoryRow(t *testing.T) {
	for _, language := range uilang.Languages() {
		found := false
		for _, row := range settingsRowsFor(language.Code) {
			if row.key != "memory" {
				continue
			}
			found = true
			if row.kind != setTriState || row.nextSession {
				t.Fatalf("%s: memory row kind=%v nextSession=%v", language.Code, row.kind, row.nextSession)
			}
			if row.label == "" || row.label == "tui.setting_memory.label" || row.desc == "" || row.desc == "tui.setting_memory.desc" {
				t.Fatalf("%s: memory row untranslated: %q / %q", language.Code, row.label, row.desc)
			}
		}
		if !found {
			t.Fatalf("%s: memory row missing", language.Code)
		}
	}
	var c config.TomlConfig
	settingToggleKey(&c, "memory")
	if c.Memory.Enabled == nil || !*c.Memory.Enabled {
		t.Fatalf("first toggle = %v, want on", c.Memory.Enabled)
	}
	settingToggleKey(&c, "memory")
	if c.Memory.Enabled == nil || *c.Memory.Enabled {
		t.Fatalf("second toggle = %v, want off", c.Memory.Enabled)
	}
	settingResetKey(&c, "memory")
	if c.Memory.Enabled != nil {
		t.Fatal("reset must clear the memory switch")
	}
}
