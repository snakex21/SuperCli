package tui

import (
	"testing"

	"supercli/internal/llm"
)

func TestSettingsTaskReasoningEffort(t *testing.T) {
	m := newSettingsModel(t, "task_reasoning_effort = \"low\"\nreasoning_effort = \"medium\"\n")
	m = cursorForKey(m, "task_reasoning_effort")
	next, _ := m.settingsEnter()
	m = next.(Model)
	if m.menu.editBuf != "low" {
		t.Fatalf("raw effort = %q", m.menu.editBuf)
	}
	previous := llm.ReasoningEffort()
	m.menu.editBuf = "invalid"
	next, _ = m.settingsCommitText()
	m = next.(Model)
	if m.menu.formErr == "" || loadCfg(t, m).TaskReasoningEffort != "low" {
		t.Fatal("invalid effort accepted")
	}
	for _, value := range []string{"max", "default", "high", ""} {
		m.menu.editBuf = value
		next, _ = m.settingsCommitText()
		m = next.(Model)
		want := value
		if want == "default" {
			want = ""
		}
		got := loadCfg(t, m)
		if got.TaskReasoningEffort != want || got.ReasoningEffort != "medium" {
			t.Fatalf("effort roundtrip: %+v", got)
		}
	}
	if llm.ReasoningEffort() != previous {
		t.Fatal("worker editor changed global effort")
	}
}
