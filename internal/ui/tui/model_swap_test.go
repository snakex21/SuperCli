package tui

import (
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/llm"
	"supercli/internal/llm/providers"
	"supercli/internal/system/config"
)

// newPickerModelWithMgr builds a TUI model wired to a real providers.Manager
// pointed at a temp config.toml, plus a single selectable model.
func newPickerModelWithMgr(t *testing.T) (Model, *providers.Manager, string) {
	t.Helper()
	home := t.TempDir()
	cfgPath := filepath.Join(home, "config.toml")
	if err := config.SaveToml(cfgPath, config.TomlConfig{
		Providers: []config.ProviderConf{{Name: "anthropic", Type: "openai", BaseURL: "http://x", Model: "old"}},
	}); err != nil {
		t.Fatal(err)
	}
	mgr := providers.NewManager(home)
	mgr.SetActiveConfigPath(cfgPath)
	mgr.Reload()

	m := New(Options{
		Home:         "/x",
		ModelSwapper: &testModelSwapper{current: "old"},
		ProviderMgr:  mgr,
		ModelLister: testModelLister{models: []llm.ModelInfo{
			{ID: "claude-sonnet", Provider: "anthropic", ToolUse: true},
		}},
		ModelSwapFn: func(modelID, provider string) (llm.Provider, error) { return stubLLM{n: modelID}, nil },
	})
	return m, mgr, cfgPath
}

// TestPickerConfirm_PersistsImmediately is the regression test for the
// reported bug: picking a model in the /model picker must persist the
// choice to config the instant Enter is pressed — NOT only after the
// user later sends a message. We confirm the picker and assert the
// config was written without ever starting a run or feeding any further
// message into Update.
func TestPickerConfirm_PersistsImmediately(t *testing.T) {
	m, mgr, _ := newPickerModelWithMgr(t)

	m.input.SetValue("/model")
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // open picker
	mm := out.(Model)
	if mm.mode != modeMenu || mm.menu.kind != menuModels {
		t.Fatalf("did not open models picker: mode=%v kind=%v", mm.mode, mm.menu.kind)
	}

	out, cmd := mm.Update(tea.KeyMsg{Type: tea.KeyEnter}) // confirm selection
	mm = out.(Model)

	// Persisted synchronously at confirm — no message round-trip needed.
	if got := mgr.LoadActiveModel(); got != "claude-sonnet" {
		t.Fatalf("after picker confirm, saved model = %q, want claude-sonnet (must persist at confirm, not at send)", got)
	}
	// Menu closed and provider swapped on the agent loop / header.
	if mm.mode != modeNormal {
		t.Fatalf("picker should close after confirm, mode=%v", mm.mode)
	}
	if mm.llm == nil || mm.llm.Name() != "claude-sonnet" {
		t.Fatalf("header LLM not swapped: %+v", mm.llm)
	}
	// Whatever cmd is returned must not, on its own, be required for
	// persistence — already asserted above. (cmd may be nil.)
	_ = cmd
}

// TestPickerCancel_DoesNotPersist ensures that opening the picker and
// pressing Esc changes/saves NOTHING: the active model stays unset and
// the provider is never rebuilt.
func TestPickerCancel_DoesNotPersist(t *testing.T) {
	m, mgr, _ := newPickerModelWithMgr(t)

	m.input.SetValue("/model")
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // open picker
	mm := out.(Model)

	swapCalled := false
	mm.modelSwapFn = func(modelID, provider string) (llm.Provider, error) {
		swapCalled = true
		return stubLLM{n: modelID}, nil
	}

	out, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEsc}) // cancel
	mm = out.(Model)

	if mm.mode != modeNormal {
		t.Fatalf("esc should close picker, mode=%v", mm.mode)
	}
	if swapCalled {
		t.Fatal("cancel must NOT rebuild the provider")
	}
	if got := mgr.LoadActiveModel(); got != "" {
		t.Fatalf("cancel must NOT persist a model, got %q", got)
	}
}
