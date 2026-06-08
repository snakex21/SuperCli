package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/config"
	"supercli/internal/llm"
	"supercli/internal/providers"
)

// ---------- configuredProviderNames -----------------------------------------

func TestConfiguredProviderNames(t *testing.T) {
	home := t.TempDir()
	mgr := providers.NewManager(home)
	mgr.Add("lmstudio", "openai", "http://localhost:1234/v1", "", "")
	mgr.Add("openai", "openai", "https://api.openai.com/v1", "sk-test", "")

	m := New(Options{ProviderMgr: mgr})
	names := m.configuredProviderNames()
	if len(names) != 2 {
		t.Fatalf("configuredProviderNames = %d, want 2", len(names))
	}
	if names[0] != "lmstudio" || names[1] != "openai" {
		t.Fatalf("names = %v, want [lmstudio openai]", names)
	}
}

func TestConfiguredProviderNames_Empty(t *testing.T) {
	m := New(Options{})
	names := m.configuredProviderNames()
	if len(names) != 0 {
		t.Fatalf("configuredProviderNames = %d, want 0", len(names))
	}
}

// ---------- filteredModelRows — configured provider filter ------------------

func TestFilteredModelRows_OnlyConfiguredProviders(t *testing.T) {
	home := t.TempDir()
	mgr := providers.NewManager(home)
	mgr.Add("lmstudio", "openai", "http://localhost:1234/v1", "", "")
	mgr.Reload()

	// Build a capability registry with mixed models:
	// - "qwen" belongs to lmstudio (configured)
	// - "gpt-4o" belongs to openai (NOT configured — seed model)
	// - "claude" belongs to anthropic (NOT configured — seed model)
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "qwen3.6", Provider: "lmstudio", ToolUse: true, Stream: true})
	caps.Register(llm.ModelInfo{ID: "gpt-4o", Provider: "openai", ToolUse: true, Stream: true})
	caps.Register(llm.ModelInfo{ID: "claude-3", Provider: "anthropic", ToolUse: true, Stream: true})

	m := New(Options{
		ProviderMgr:        mgr,
		CapabilityRegistry: caps,
	})
	m.menu = interactiveMenu{kind: menuModels}

	rows := m.filteredModelRows()
	if len(rows) != 1 {
		t.Fatalf("filteredModelRows = %d, want 1 (only lmstudio)", len(rows))
	}
	if rows[0].ID != "qwen3.6" {
		t.Fatalf("only model should be qwen3.6, got %q", rows[0].ID)
	}
}

func TestFilteredModelRows_DoesNotShowSeedModelsForConfiguredProviders(t *testing.T) {
	home := t.TempDir()
	mgr := providers.NewManager(home)
	mgr.Add("deepseek", "openai", "https://api.deepseek.com/v1", "bad-key", "")
	mgr.Reload()

	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "deepseek-r1", Provider: "deepseek", Source: llm.SourceSeed})
	caps.Register(llm.ModelInfo{ID: "deepseek-chat", Provider: "deepseek", Source: llm.SourceProvider})

	m := New(Options{
		ProviderMgr:        mgr,
		CapabilityRegistry: caps,
	})
	m.menu = interactiveMenu{kind: menuProviderModels, provider: "deepseek"}

	rows := m.filteredModelRows()
	if len(rows) != 1 || rows[0].ID != "deepseek-chat" {
		t.Fatalf("rows = %+v, want only scanned deepseek-chat", rows)
	}
}

func TestFilteredModelRows_NoProviders_ShowsAll(t *testing.T) {
	// When no providers are configured, don't filter —
	// show all models (useful before user adds a provider).
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "gpt-4o", Provider: "openai", ToolUse: true, Stream: true})
	caps.Register(llm.ModelInfo{ID: "claude-3", Provider: "anthropic", ToolUse: true, Stream: true})

	m := New(Options{
		CapabilityRegistry: caps,
		// No ProviderMgr set.
	})
	m.menu = interactiveMenu{kind: menuModels}

	rows := m.filteredModelRows()
	if len(rows) != 2 {
		t.Fatalf("filteredModelRows = %d, want 2 (no providers configured)", len(rows))
	}
}

// ---------- filteredModelRows — hidden models filter ------------------------

func TestFilteredModelRows_HidesDisabledModelsInGlobalView(t *testing.T) {
	home := t.TempDir()
	mgr := providers.NewManager(home)
	mgr.Add("lmstudio", "openai", "http://localhost:1234/v1", "", "")
	mgr.Reload()
	mgr.HideModel("gemma") // mark gemma as hidden

	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "qwen3.6", Provider: "lmstudio", ToolUse: true, Stream: true})
	caps.Register(llm.ModelInfo{ID: "gemma", Provider: "lmstudio", ToolUse: true, Stream: true})

	m := New(Options{
		ProviderMgr:        mgr,
		CapabilityRegistry: caps,
	})
	m.menu = interactiveMenu{kind: menuModels} // GLOBAL view

	rows := m.filteredModelRows()
	if len(rows) != 1 {
		t.Fatalf("filteredModelRows = %d, want 1 (gemma hidden)", len(rows))
	}
	if rows[0].ID != "qwen3.6" {
		t.Fatalf("expected qwen3.6, got %q", rows[0].ID)
	}
}

func TestFilteredModelRows_ShowsDisabledModelsInProviderView(t *testing.T) {
	home := t.TempDir()
	mgr := providers.NewManager(home)
	mgr.Add("lmstudio", "openai", "http://localhost:1234/v1", "", "")
	mgr.Reload()
	mgr.HideModel("gemma")

	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "qwen3.6", Provider: "lmstudio", ToolUse: true, Stream: true})
	caps.Register(llm.ModelInfo{ID: "gemma", Provider: "lmstudio", ToolUse: true, Stream: true})

	m := New(Options{
		ProviderMgr:        mgr,
		CapabilityRegistry: caps,
	})
	m.menu = interactiveMenu{kind: menuProviderModels, provider: "lmstudio"} // PROVIDER view

	rows := m.filteredModelRows()
	if len(rows) != 2 {
		t.Fatalf("filteredModelRows = %d, want 2 (provider view shows all)", len(rows))
	}
}

// ---------- filteredModelRows — provider-specific filtering -----------------

func TestFilteredModelRows_FiltersByProvider(t *testing.T) {
	home := t.TempDir()
	mgr := providers.NewManager(home)
	mgr.Add("lmstudio", "openai", "http://localhost:1234/v1", "", "")
	mgr.Add("openai", "openai", "https://api.openai.com/v1", "sk-test", "")
	mgr.Reload()

	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "qwen3.6", Provider: "lmstudio", ToolUse: true, Stream: true})
	caps.Register(llm.ModelInfo{ID: "gpt-4o", Provider: "openai", ToolUse: true, Stream: true})

	m := New(Options{
		ProviderMgr:        mgr,
		CapabilityRegistry: caps,
	})
	m.menu = interactiveMenu{kind: menuProviderModels, provider: "lmstudio"}

	rows := m.filteredModelRows()
	if len(rows) != 1 {
		t.Fatalf("filteredModelRows = %d, want 1 (only lmstudio)", len(rows))
	}
	if rows[0].ID != "qwen3.6" {
		t.Fatalf("expected qwen3.6, got %q", rows[0].ID)
	}
}

// ---------- activeProviderName ----------------------------------------------

func TestActiveProviderName(t *testing.T) {
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "qwen3.6", Provider: "lmstudio", ToolUse: true, Stream: true})

	swapper := &testModelSwapper{current: "qwen3.6"}

	m := New(Options{
		CapabilityRegistry: caps,
		ModelSwapper:       swapper,
	})

	name := m.activeProviderName()
	if name != "lmstudio" {
		t.Fatalf("activeProviderName = %q, want lmstudio", name)
	}
}

func TestActiveProviderName_UnknownModel(t *testing.T) {
	caps := llm.NewCapabilityRegistry()

	swapper := &testModelSwapper{current: "nonexistent"}

	m := New(Options{
		CapabilityRegistry: caps,
		ModelSwapper:       swapper,
	})

	name := m.activeProviderName()
	if name != "" {
		t.Fatalf("activeProviderName = %q, want empty", name)
	}
}

// ---------- persistence round-trip ------------------------------------------

func TestSaveActiveConfigThenLoadHiddenState(t *testing.T) {
	home := t.TempDir()
	mgr := providers.NewManager(home)
	mgr.Add("lmstudio", "openai", "http://localhost:1234/v1", "", "")

	// Save active config (simulates selecting a model in TUI).
	if err := mgr.SaveActiveConfig("qwen3.6", "lmstudio"); err != nil {
		t.Fatalf("SaveActiveConfig: %v", err)
	}

	// Hide a model.
	mgr.HideModel("gemma")

	// Simulate restart: create new manager, reload, load hidden.
	mgr2 := providers.NewManager(home)
	mgr2.Reload()
	mgr2.LoadHiddenState()

	// Verify active model persisted.
	if got := mgr2.LoadActiveModel(); got != "qwen3.6" {
		t.Fatalf("LoadActiveModel = %q, want qwen3.6", got)
	}

	// Verify hidden state persisted.
	if !mgr2.IsHidden("gemma") {
		t.Fatal("gemma should still be hidden after restart")
	}

	// Verify config.toml has all fields.
	globalPath, _ := config.FindTomlPaths(home, ".")
	tc, err := config.LoadToml(globalPath)
	if err != nil {
		t.Fatalf("LoadToml: %v", err)
	}
	if tc.DefaultModel != "qwen3.6" {
		t.Fatalf("default_model = %q", tc.DefaultModel)
	}
	if tc.DefaultProvider != "lmstudio" {
		t.Fatalf("default_provider = %q", tc.DefaultProvider)
	}
	if len(tc.HiddenModels) != 1 || tc.HiddenModels[0] != "gemma" {
		t.Fatalf("hidden_models = %v", tc.HiddenModels)
	}
}

func TestProviderFormLettersAreTextNotNavigation(t *testing.T) {
	m := New(Options{})
	m.mode = modeMenu
	m.menu = interactiveMenu{kind: menuProviderForm, form: []string{"", "", "", ""}, formAt: 0}

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	mm := out.(Model)
	if mm.menu.formAt != 0 {
		t.Fatalf("k moved formAt to %d, want 0", mm.menu.formAt)
	}
	if mm.menu.form[0] != "k" {
		t.Fatalf("form[0] = %q, want k", mm.menu.form[0])
	}

	out, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	mm = out.(Model)
	if mm.menu.formAt != 0 {
		t.Fatalf("j moved formAt to %d, want 0", mm.menu.formAt)
	}
	if mm.menu.form[0] != "kj" {
		t.Fatalf("form[0] = %q, want kj", mm.menu.form[0])
	}
}

func TestProviderFormArrowKeysStillNavigate(t *testing.T) {
	m := New(Options{})
	m.mode = modeMenu
	m.menu = interactiveMenu{kind: menuProviderForm, form: []string{"", "", "", ""}, formAt: 0}

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm := out.(Model)
	if mm.menu.formAt != 1 {
		t.Fatalf("down formAt = %d, want 1", mm.menu.formAt)
	}
	out, _ = mm.Update(tea.KeyMsg{Type: tea.KeyUp})
	mm = out.(Model)
	if mm.menu.formAt != 0 {
		t.Fatalf("up formAt = %d, want 0", mm.menu.formAt)
	}
}

func TestProviderFormAltAsciiIgnoredButAltGrTextAllowed(t *testing.T) {
	m := New(Options{})
	m.mode = modeMenu
	m.menu = interactiveMenu{kind: menuProviderForm, form: []string{"", "", "", ""}, formAt: 0}

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}, Alt: true})
	mm := out.(Model)
	if mm.menu.form[0] != "" {
		t.Fatalf("alt+a inserted %q, want empty", mm.menu.form[0])
	}

	out, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'ą'}, Alt: true})
	mm = out.(Model)
	if mm.menu.form[0] != "ą" {
		t.Fatalf("altgr char form[0] = %q, want ą", mm.menu.form[0])
	}
}

func TestProviderFormTerminalPasteAppendsText(t *testing.T) {
	m := New(Options{})
	m.mode = modeMenu
	m.menu = interactiveMenu{kind: menuProviderForm, form: []string{"", "", "", ""}, formAt: 2}

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("http://localhost:1234/v1\n"), Paste: true})
	mm := out.(Model)
	if mm.menu.form[2] != "http://localhost:1234/v1" {
		t.Fatalf("pasted form[2] = %q", mm.menu.form[2])
	}
}

func TestProviderFormSaveScansProviderImmediately(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q, want /v1/models", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("auth = %q, want Bearer test-key", r.Header.Get("Authorization"))
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "deepseek-chat"}}})
	}))
	defer srv.Close()

	mgr := providers.NewManager(t.TempDir())
	caps := llm.NewCapabilityRegistry()
	m := New(Options{ProviderMgr: mgr, CapabilityRegistry: caps})
	m.mode = modeMenu
	m.menu = interactiveMenu{
		kind:   menuProviderForm,
		form:   []string{"deepseek", "openai", srv.URL + "/v1", "test-key"},
		formAt: 3,
	}

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := out.(Model)
	if mm.menu.kind != menuProviders {
		t.Fatalf("menu kind = %v, want providers", mm.menu.kind)
	}
	mi, ok := caps.Get("deepseek-chat")
	if !ok {
		t.Fatal("deepseek-chat not scanned after saving provider")
	}
	if mi.Provider != "deepseek" || mi.Source != llm.SourceProvider {
		t.Fatalf("model info = %+v", mi)
	}
}

// ---------- helpers ---------------------------------------------------------

func init() {
	// Ensure test temp files don't leak.
	os.Setenv("SUPERCLI_DEBUG", "0")
}
