package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/system/config"
)

func generationSpeedModel(value *bool) Model {
	m := New(Options{Language: "en", NoColor: true, ShowGenerationSpeed: value})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return next.(Model)
}

func finishGenerationSpeedTurn(m Model, event agent.DoneEvent) Model {
	next, _ := m.handleAgentEvent(agent.MessageEvent{Text: "Completed answer."})
	next, _ = next.(Model).handleAgentEvent(event)
	return next.(Model)
}

func measuredGenerationSpeedEvent() agent.DoneEvent {
	return agent.DoneEvent{
		Usage:            agent.Usage{Input: 100, Output: 900, Reasoning: 300},
		GenerationTokens: 90, GenerationDuration: 3 * time.Second,
	}
}

func TestGenerationSpeedDoneEventToView(t *testing.T) {
	m := generationSpeedModel(nil)
	next, _ := m.handleAgentEvent(agent.ReasoningEvent{Text: "A short thought."})
	next, _ = next.(Model).handleAgentEvent(agent.MessageEvent{Text: "Completed answer."})
	m = next.(Model)
	if strings.Contains(m.View(), "tok/s") {
		t.Fatal("generation speed appeared before completion")
	}
	event := measuredGenerationSpeedEvent()
	next, _ = m.handleAgentEvent(event)
	m = next.(Model)
	want := ansi.Strip(m.marker.DoneEst(100, 900, false)) + " · " + fmt.Sprintf(textFor("en", "generation.rate"), 30.0)
	if view := ansi.Strip(m.View()); !strings.Contains(view, want) || !strings.Contains(view, "Completed answer.") {
		t.Fatalf("backend measured rate missing beside completion marker: want %q in %q", want, view)
	}
	if m.chat.msgs[len(m.chat.msgs)-1].generationSpeed != 30 {
		t.Fatal("rate used total turn usage or visible text instead of backend generation measurement")
	}
}

func TestGenerationSpeedDisabledAndUnmeasured(t *testing.T) {
	off := false
	for _, tc := range []struct {
		name       string
		preference *bool
		event      agent.DoneEvent
	}{
		{"disabled", &off, measuredGenerationSpeedEvent()},
		{"no generation usage", nil, agent.DoneEvent{Usage: agent.Usage{Input: 10, Output: 50}, GenerationDuration: time.Second}},
		{"no duration", nil, agent.DoneEvent{Usage: agent.Usage{Input: 10, Output: 50}, GenerationTokens: 50}},
		{"instantaneous", nil, agent.DoneEvent{Usage: agent.Usage{Input: 10, Output: 50}, GenerationTokens: 50, GenerationDuration: time.Nanosecond}},
		{"estimated visible tokens", nil, agent.DoneEvent{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := finishGenerationSpeedTurn(generationSpeedModel(tc.preference), tc.event)
			if view := m.View(); strings.Contains(view, "tok/s") || !strings.Contains(view, "Completed answer.") {
				t.Fatalf("unreliable/disabled speed affected completion: %q", view)
			}
		})
	}
}

func generationSpeedPortableModel(t *testing.T, seed string) Model {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadToml(path)
	if err != nil {
		t.Fatal(err)
	}
	m := New(Options{Home: dir, DataDir: dir, Language: "en", NoColor: true, ShowGenerationSpeed: cfg.ShowGenerationSpeed})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return next.(Model)
}

func TestGenerationSpeedSettingAppliesLiveAndReloadsPortable(t *testing.T) {
	m := finishGenerationSpeedTurn(generationSpeedPortableModel(t, "show_generation_speed = false\nlanguage = \"en\"\n"), measuredGenerationSpeedEvent())
	if strings.Contains(m.View(), "tok/s") {
		t.Fatal("initial portable off preference was ignored")
	}
	next, _ := m.openSettingsMenu()
	m = cursorForKey(next.(Model), "show_generation_speed")
	rows := m.localizedSettingsRows()
	row := rows[m.menu.cursor]
	if settingCategory(row.key) != 0 || row.nextSession || row.label == row.key || row.desc == "" {
		t.Fatalf("speed preference is not a localized live general setting: %+v", row)
	}
	for _, want := range []struct {
		value   *bool
		visible bool
	}{
		{nil, true}, // off -> default/on
		{func() *bool { v := true; return &v }(), true},
		{func() *bool { v := false; return &v }(), false},
		{nil, true},
	} {
		next, _ = m.settingsEnter()
		m = next.(Model)
		cfg := loadCfg(t, m)
		if (cfg.ShowGenerationSpeed == nil) != (want.value == nil) || (want.value != nil && *cfg.ShowGenerationSpeed != *want.value) {
			t.Fatalf("unexpected persisted tri-state: %v", cfg.ShowGenerationSpeed)
		}
		if got := strings.Contains(m.viewport.View(), "tok/s"); got != want.visible {
			t.Fatalf("existing completion did not update live: visible=%v, want %v", got, want.visible)
		}
		reloaded := New(Options{Home: m.home, DataDir: m.dataDir, Language: "en", NoColor: true, ShowGenerationSpeed: cfg.ShowGenerationSpeed})
		reloaded.width, reloaded.height = 120, 40
		reloaded = finishGenerationSpeedTurn(reloaded, measuredGenerationSpeedEvent())
		if got := strings.Contains(reloaded.View(), "tok/s"); got != want.visible {
			t.Fatalf("portable config reload changed speed preference: visible=%v, want %v", got, want.visible)
		}
	}
	if m.settingsGlobalPath() != filepath.Join(m.dataDir, "config.toml") {
		t.Fatal("speed settings escaped the portable application data directory")
	}
	next, _ = m.closeMenu()
	if !strings.Contains(next.(Model).View(), "tok/s") {
		t.Fatal("closing settings lost the immediately updated completion")
	}
}

func TestGenerationSpeedResetAndResumePreserveDefault(t *testing.T) {
	for _, resetAll := range []bool{false, true} {
		m := finishGenerationSpeedTurn(generationSpeedPortableModel(t, "show_generation_speed = false\n"), measuredGenerationSpeedEvent())
		next, _ := m.openSettingsMenu()
		m = next.(Model)
		if resetAll {
			m = cursorForKey(m, "")
			next, _ = m.settingsEnter()
		} else {
			m = cursorForKey(m, "show_generation_speed")
			next, _ = m.settingsResetCurrent()
		}
		m = next.(Model)
		if loadCfg(t, m).ShowGenerationSpeed != nil || !strings.Contains(m.viewport.View(), "tok/s") {
			t.Fatal("reset did not restore the default visible rate immediately")
		}
		raw, err := os.ReadFile(m.settingsGlobalPath())
		if err != nil || strings.Contains(string(raw), "show_generation_speed =") {
			t.Fatal("reset left an explicit speed setting in portable config")
		}
	}
	off := false
	m := generationSpeedModel(&off)
	m.applyResumedTranscript(&resumedTranscript{ID: "restored", Messages: []llm.Message{{Role: llm.RoleAssistant, Content: "Prior answer."}}, Seqs: []int{1}})
	if !m.chat.hideGenerationSpeed || strings.Contains(m.View(), "tok/s") {
		t.Fatal("resume lost the off preference or invented historical timing")
	}
	m = finishGenerationSpeedTurn(m, measuredGenerationSpeedEvent())
	if strings.Contains(m.View(), "tok/s") {
		t.Fatal("first resumed completion ignored the off preference")
	}
}

func TestGenerationSpeedCopiedChatInvalidatesCompletedPrefix(t *testing.T) {
	original := newChat(100, "en")
	p := DefaultPalette()
	original.addCompletion("completion", 30)
	original.renderCompleted(p)
	copied := original
	copied.hideGenerationSpeed = true
	copied.completedDirty = true
	copied.addSystem("later event")
	if rendered := copied.renderCompleted(p); strings.Contains(rendered, "tok/s") || !strings.Contains(rendered, "later event") {
		t.Fatal("completed-prefix cache retained a rate after disabling it")
	}
	if !strings.Contains(original.renderCompleted(p), "tok/s") {
		t.Fatal("copied chat changed the original presentation or completion metadata")
	}
	copied.hideGenerationSpeed = false
	copied.completedDirty = true
	if !strings.Contains(copied.renderCompleted(p), "tok/s") {
		t.Fatal("completion measurement was discarded instead of hidden")
	}
}
