package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"supercli/internal/llm"
	"supercli/internal/llm/providers"
	"supercli/internal/system/config"
)

func TestToggleReasoningMenuNormalizesLegacySelectionAndKeyboard(t *testing.T) {
	t.Cleanup(func() { _ = llm.SetReasoningEffort("") })
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "qwen", ReasoningToggleOnly: true, Reasoning: true})
	p, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: "http://localhost:1234/v1", Model: "qwen", Capabilities: caps})
	if err != nil {
		t.Fatal(err)
	}
	m := New(Options{LLM: p, DataDir: t.TempDir(), Language: "pl"})
	_ = llm.SetReasoningEffort("low")
	out, _ := m.openReasoningMenu()
	m = out.(Model)
	opts := m.localizedReasoningMenuOptions()
	if len(opts) != 3 || opts[1].Label != "Wyłączone" || opts[2].Label != "Włączone" || m.menu.cursor != 2 {
		t.Fatalf("options=%+v cursor=%d", opts, m.menu.cursor)
	}
	view := m.renderReasoningMenu()
	for _, unwanted := range []string{"niskie", "wysokie", "maksymalne"} {
		if strings.Contains(view, unwanted) {
			t.Errorf("toggle exposes graded level %q", unwanted)
		}
	}
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = out.(Model)
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = out.(Model)
	if llm.ReasoningEffort() != "none" {
		t.Fatalf("keyboard selected %q", llm.ReasoningEffort())
	}
	out, _ = m.openReasoningMenu()
	m = out.(Model)
	m.menu.cursor = 99
	m.clampMenuCursor()
	if m.menu.cursor != 2 {
		t.Fatalf("cursor=%d", m.menu.cursor)
	}
}

func TestReasoningMenuPersistsActiveProjectConfig(t *testing.T) {
	t.Cleanup(func() { _ = llm.SetReasoningEffort("") })
	data := t.TempDir()
	active := filepath.Join(t.TempDir(), ".supercli", "config.toml")
	if err := config.SaveToml(active, config.TomlConfig{ReasoningEffort: "low", DefaultModel: "keep-model"}); err != nil {
		t.Fatal(err)
	}
	manager := providers.NewManager(data)
	manager.SetActiveConfigPath(active)
	p, _ := newStubLLM("gpt-5.5")
	m := New(Options{LLM: p, DataDir: data, ProviderMgr: manager})
	out, _ := m.openReasoningMenu()
	m = out.(Model)
	m.menu.cursor = m.reasoningOptionIndex("high")
	m.selectReasoningEffort()
	for _, path := range []string{active, filepath.Join(data, "config.toml")} {
		cfg, err := config.LoadToml(path)
		if err != nil || cfg.ReasoningEffort != "high" {
			t.Fatalf("config=%+v error=%v", cfg, err)
		}
	}
	cfg, _ := config.LoadToml(active)
	if cfg.DefaultModel != "keep-model" {
		t.Fatal("unrelated config changed")
	}
}
