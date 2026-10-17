package tui

import (
	"supercli/internal/llm"
	"testing"
)

func TestSettingsPurposeReasoningEffort(t *testing.T) {
	initial := llm.ReasoningEffort()
	for _, key := range []string{"advisor_reasoning_effort", "review_reasoning_effort", "darwin_reasoning_effort"} {
		t.Run(key, func(t *testing.T) {
			m := newSettingsModel(t, key+" = \"low\"\nreasoning_effort = \"medium\"\n")
			m = cursorForKey(m, key)
			next, _ := m.settingsEnter()
			m = next.(Model)
			if m.menu.editBuf != "low" {
				t.Fatalf("raw = %q", m.menu.editBuf)
			}
			m.menu.editBuf = "invalid"
			next, _ = m.settingsCommitText()
			m = next.(Model)
			saved := loadCfg(t, m)
			if m.menu.formErr == "" || settingTextValue(&saved, key) != "low" {
				t.Fatal("invalid effort accepted")
			}
			for _, want := range append(append([]string{}, llm.ReasoningEffortLevels...), "") {
				m.menu.editBuf = want
				next, _ = m.settingsCommitText()
				m = next.(Model)
				got := loadCfg(t, m)
				if settingTextValue(&got, key) != want || got.ReasoningEffort != "medium" {
					t.Fatal("effort roundtrip/global changed")
				}
			}
		})
	}
	if llm.ReasoningEffort() != initial {
		t.Fatal("editor changed global")
	}
}
