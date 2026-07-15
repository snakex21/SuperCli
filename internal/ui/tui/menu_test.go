package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"supercli/internal/llm"
	"supercli/internal/llm/providers"
	"supercli/internal/system/config"
)

func TestProviderStatusCellIncludesMeasuredLatency(t *testing.T) {
	m := New(Options{NoColor: true})
	m.providerStatuses = map[string]providerStatus{
		"local": {checked: true, online: true, latency: 42 * time.Millisecond},
	}
	plain, _ := m.providerStatusCell("local")
	if plain != "online · 42ms" {
		t.Fatalf("status=%q", plain)
	}
}

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

func TestReasoningMenu_CtrlROpensModal(t *testing.T) {
	t.Cleanup(func() { _ = llm.SetReasoningEffort(""); llm.SetReasoningEffortSupport("gpt-5.5", nil) })
	llmProvider, _ := newStubLLM("gpt-5.5")
	m := New(Options{Home: t.TempDir(), DataDir: t.TempDir(), LLM: llmProvider})
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	mm := out.(Model)
	if mm.mode != modeMenu || mm.menu.kind != menuReasoning {
		t.Fatalf("mode/kind = %v/%v, want menu/reasoning", mm.mode, mm.menu.kind)
	}
	view := mm.renderMenuView()
	for _, want := range []string{"Reasoning effort", "gpt-5.5", "low", "xhigh"} {
		if !strings.Contains(view, want) {
			t.Fatalf("reasoning menu missing %q:\n%s", want, view)
		}
	}
}

func TestReasoningMenu_EnterPersistsSelection(t *testing.T) {
	t.Cleanup(func() { _ = llm.SetReasoningEffort(""); llm.SetReasoningEffortSupport("gpt-5.5", nil) })
	llmProvider, _ := newStubLLM("gpt-5.5")
	m := New(Options{Home: t.TempDir(), DataDir: t.TempDir(), LLM: llmProvider})
	mm, _ := m.openReasoningMenu()
	m = mm.(Model)
	m.menu.cursor = reasoningOptionIndex("low")
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := out.(Model)
	if got.mode != modeNormal {
		t.Fatalf("mode = %v, want normal", got.mode)
	}
	if eff := llm.ReasoningEffort(); eff != "low" {
		t.Fatalf("ReasoningEffort = %q, want low", eff)
	}
	if !strings.Contains(got.statusOverride, "low") {
		t.Fatalf("statusOverride = %q, want low", got.statusOverride)
	}
}

func TestRenderModelsMenu_EnrichesProviderModelPricing(t *testing.T) {
	home := t.TempDir()
	mgr := providers.NewManager(home)
	mgr.Add("deepseek", "openai", "https://api.deepseek.com/v1", "k", "")
	mgr.Reload()

	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "deepseek-chat", Provider: "deepseek", Source: llm.SourceProvider})
	caps.Register(llm.ModelInfo{ID: "deepseek/deepseek-chat", ContextLength: 64000, InputCost: 70, OutputCost: 270, Source: llm.SourceExternal})

	m := New(Options{ProviderMgr: mgr, CapabilityRegistry: caps})
	m.menu = interactiveMenu{kind: menuProviderModels, provider: "deepseek"}
	out := m.renderMenuView()
	for _, want := range []string{"64k", "$70.00", "$270.00"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered models menu missing %q:\n%s", want, out)
		}
	}
}

func TestRenderModelsMenu_EnrichesContextFromOpenRouterSuffix(t *testing.T) {
	home := t.TempDir()
	mgr := providers.NewManager(home)
	mgr.Add("local", "openai", "http://localhost:1234/v1", "", "")
	mgr.Reload()

	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "deepseek-v4-flash", Provider: "local", Source: llm.SourceProvider})
	caps.Register(llm.ModelInfo{ID: "deepseek/deepseek-v4-flash", ContextLength: 1048576, Source: llm.SourceExternal})

	m := New(Options{ProviderMgr: mgr, CapabilityRegistry: caps})
	m.menu = interactiveMenu{kind: menuProviderModels, provider: "local"}
	out := m.renderMenuView()
	if !strings.Contains(out, "1048k") {
		t.Fatalf("rendered models menu missing OpenRouter context metadata:\n%s", out)
	}
}

func TestRenderModelsMenu_CodexShowsSubscriptionNotUSD(t *testing.T) {
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "gpt-5.5", Provider: "codex", ContextLength: 272000, InputCost: 1250, OutputCost: 10000, Source: llm.SourceExternal})
	m := New(Options{CapabilityRegistry: caps})
	m.menu = interactiveMenu{kind: menuModels}
	out := m.renderMenuView()
	if !strings.Contains(out, "sub") {
		t.Fatalf("codex model should show subscription marker, got:\n%s", out)
	}
	if strings.Contains(out, "$1250") || strings.Contains(out, "$10000") {
		t.Fatalf("codex model should not show API dollar prices, got:\n%s", out)
	}
}

func TestModelAndModelsHaveDistinctVisibilitySemantics(t *testing.T) {
	home := t.TempDir()
	mgr := providers.NewManager(home)
	if err := mgr.Add("local", "openai", "http://localhost:1234/v1", "", ""); err != nil {
		t.Fatal(err)
	}
	mgr.Reload()
	mgr.HideModelFor("local", "gemma")
	caps := llm.NewCapabilityRegistry()
	for _, id := range []string{"gemma", "qwen"} {
		caps.Register(llm.ModelInfo{ID: id, Provider: "local", Source: llm.SourceProvider, ToolUse: true})
	}
	m := New(Options{ProviderMgr: mgr, CapabilityRegistry: caps})

	// /model is the fast picker: hidden models stay out.
	m.input.SetValue("/model")
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	picker := out.(Model)
	pickerView := picker.renderMenuView()
	if picker.menu.kind != menuModels || strings.Contains(pickerView, "gemma") || !strings.Contains(pickerView, "qwen") {
		t.Fatalf("/model should contain only enabled models:\n%s", picker.renderMenuView())
	}
	if strings.Contains(pickerView, "[on]") || strings.Contains(pickerView, "[off]") {
		t.Fatalf("/model is a picker and should not render visibility switches:\n%s", pickerView)
	}
	picker.menu.filter = "qwen"
	out, _ = picker.Update(tea.KeyMsg{Type: tea.KeyLeft})
	picker = out.(Model)
	if mgr.IsHiddenFor("local", "qwen") {
		t.Fatal("Left Arrow in /model must not disable a model")
	}

	// /models is the full catalog: hidden models remain reachable and Enter
	// toggles them back on without selecting/swapping the active model.
	m.input.SetValue("/models")
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	catalog := out.(Model)
	if catalog.menu.kind != menuModelCatalog || !strings.Contains(catalog.renderMenuView(), "gemma") || !strings.Contains(catalog.renderMenuView(), "[off]") {
		t.Fatalf("/models should expose the full catalog:\n%s", catalog.renderMenuView())
	}
	catalog.menu.filter = "gemma"
	catalog.menu.cursor = 0
	out, _ = catalog.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if mgr.IsHiddenFor("local", "gemma") {
		t.Fatal("Enter in /models should enable a hidden model")
	}
	out, _ = catalog.Update(tea.KeyMsg{Type: tea.KeyLeft})
	_ = out.(Model)
	if !mgr.IsHiddenFor("local", "gemma") {
		t.Fatal("Left Arrow in /models should still disable a model")
	}
}

func TestOfflineConfiguredModelRemainsInPickerAndPausedOnlyInCatalog(t *testing.T) {
	home := t.TempDir()
	mgr := providers.NewManager(home)
	if err := mgr.Add("lmstudio", "openai", "http://localhost:1234/v1", "", "local-qwen"); err != nil {
		t.Fatal(err)
	}
	mgr.Reload()
	m := New(Options{ProviderMgr: mgr, CapabilityRegistry: llm.NewCapabilityRegistry()})
	m.mode = modeMenu
	m.menu = interactiveMenu{kind: menuModels}
	if view := m.renderMenuView(); !strings.Contains(view, "local-qwen") {
		t.Fatalf("configured offline default should remain selectable:\n%s", view)
	}

	if err := mgr.SetDisabled("lmstudio", true); err != nil {
		t.Fatal(err)
	}
	mgr.Reload()
	m.menu = interactiveMenu{kind: menuModels}
	if view := m.renderMenuView(); strings.Contains(view, "local-qwen") {
		t.Fatalf("paused provider model should leave the fast picker:\n%s", view)
	}
	m.menu = interactiveMenu{kind: menuModelCatalog}
	if view := m.renderMenuView(); !strings.Contains(view, "local-qwen") || !strings.Contains(view, "provider paused") {
		t.Fatalf("paused provider model should remain manageable in the catalog:\n%s", view)
	}
}

func TestModelCatalogFilterAcceptsShortcutLettersAndBulkTogglesVisible(t *testing.T) {
	home := t.TempDir()
	mgr := providers.NewManager(home)
	if err := mgr.Add("local", "openai", "http://localhost:1234/v1", "", ""); err != nil {
		t.Fatal(err)
	}
	mgr.Reload()
	caps := llm.NewCapabilityRegistry()
	for _, id := range []string{"dreamer", "qwen"} {
		caps.Register(llm.ModelInfo{ID: id, Provider: "local", Source: llm.SourceProvider})
	}
	m := New(Options{ProviderMgr: mgr, CapabilityRegistry: caps})
	m.mode = modeMenu
	m.menu = interactiveMenu{kind: menuModelCatalog}

	// These letters used to be swallowed by provider/model shortcuts.
	for _, r := range "dream" {
		out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = out.(Model)
	}
	if m.menu.filter != "dream" || len(m.filteredModelRows()) != 1 || m.filteredModelRows()[0].ID != "dreamer" {
		t.Fatalf("catalog filter=%q rows=%v, want dreamer", m.menu.filter, m.filteredModelRows())
	}

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
	m = out.(Model)
	if !mgr.IsHiddenFor("local", "dreamer") || mgr.IsHiddenFor("local", "qwen") {
		t.Fatalf("X should disable only filtered rows: dreamer=%v qwen=%v", mgr.IsHiddenFor("local", "dreamer"), mgr.IsHiddenFor("local", "qwen"))
	}
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	_ = out.(Model)
	if mgr.IsHiddenFor("local", "dreamer") {
		t.Fatal("A should enable filtered rows")
	}
}

func TestProviderModelToggleDoesNotAffectSameIDAtAnotherProvider(t *testing.T) {
	home := t.TempDir()
	mgr := providers.NewManager(home)
	for _, name := range []string{"provider-x", "provider-y"} {
		if err := mgr.Add(name, "openai", "http://"+name+"/v1", "", "shared-model"); err != nil {
			t.Fatal(err)
		}
	}
	mgr.Reload()
	m := New(Options{ProviderMgr: mgr, CapabilityRegistry: llm.NewCapabilityRegistry()})
	m.mode = modeMenu
	m.menu = interactiveMenu{kind: menuProviderModels, provider: "provider-x"}
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = out.(Model)
	if !mgr.IsHiddenFor("provider-x", "shared-model") {
		t.Fatal("selected provider/model should be disabled")
	}
	if mgr.IsHiddenFor("provider-y", "shared-model") {
		t.Fatal("same model ID at another provider must remain enabled")
	}
}

func TestProvidersEnterOpensModelsAndSpacePauses(t *testing.T) {
	home := t.TempDir()
	mgr := providers.NewManager(home)
	if err := mgr.Add("local", "openai", "http://localhost:1234/v1", "", "qwen"); err != nil {
		t.Fatal(err)
	}
	mgr.Reload()
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "qwen", Provider: "local", Source: llm.SourceProvider})
	m := New(Options{ProviderMgr: mgr, CapabilityRegistry: caps})
	m.mode = modeMenu
	m.menu = interactiveMenu{kind: menuProviders}

	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	opened := out.(Model)
	if opened.menu.kind != menuProviderModels || opened.menu.provider != "local" || cmd == nil {
		t.Fatalf("Enter should open and refresh provider models: kind=%v provider=%q cmd=%v", opened.menu.kind, opened.menu.provider, cmd)
	}

	m.mode = modeMenu
	m.menu = interactiveMenu{kind: menuProviders}
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	paused := out.(Model)
	rows := paused.providerRows()
	if len(rows) != 1 || !rows[0].Disabled || !strings.Contains(paused.renderProvidersMenu(), "paused") {
		t.Fatalf("Space should pause provider without deleting it: %+v\n%s", rows, paused.renderProvidersMenu())
	}
}

func TestRenderModelsMenuIsVirtualizedAndNarrowSafe(t *testing.T) {
	caps := llm.NewCapabilityRegistry()
	for i := 0; i < 30; i++ {
		caps.Register(llm.ModelInfo{ID: fmt.Sprintf("model-%02d-with-a-very-long-name", i), Provider: "local", ContextLength: 131072, ToolUse: true})
	}
	m := New(Options{CapabilityRegistry: caps})
	m.palette = NoColorPalette()
	m.width, m.height = 60, 18
	m.menu = interactiveMenu{kind: menuModels, cursor: 20}
	out := m.renderMenuView()
	if !strings.Contains(out, "model-20") || strings.Contains(out, "model-00") {
		t.Fatalf("model window does not follow cursor:\n%s", out)
	}
	lines := strings.Split(out, "\n")
	if len(lines) > m.height {
		t.Fatalf("models menu uses %d rows in %d-row terminal:\n%s", len(lines), m.height, out)
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got > m.width {
			t.Fatalf("line %d width=%d exceeds menu width: %q", i+1, got, line)
		}
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
	mgr.HideModelFor("lmstudio", "gemma") // mark only this provider/model pair as hidden

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
	mgr.HideModelFor("lmstudio", "gemma")

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
	mgr.HideModelFor("lmstudio", "gemma")

	// Simulate restart: create new manager, reload, load hidden.
	mgr2 := providers.NewManager(home)
	mgr2.Reload()
	mgr2.LoadHiddenState()

	// Verify active model persisted.
	if got := mgr2.LoadActiveModel(); got != "qwen3.6" {
		t.Fatalf("LoadActiveModel = %q, want qwen3.6", got)
	}

	// Verify hidden state persisted.
	if !mgr2.IsHiddenFor("lmstudio", "gemma") {
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
	if len(tc.HiddenModels) != 1 || !strings.HasPrefix(tc.HiddenModels[0], "ref:") {
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

func TestProviderFormSaveScansProviderInBackground(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			if r.Header.Get("Authorization") != "Bearer test-key" {
				t.Errorf("auth = %q, want Bearer test-key", r.Header.Get("Authorization"))
			}
			json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "deepseek-chat"}}})
		case "/v1/chat/completions":
			// The post-save verification test request ("Say OK").
			json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]any{"content": "OK"}}},
			})
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
		}
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

	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := out.(Model)
	if mm.menu.kind != menuProviders {
		t.Fatalf("menu kind = %v, want providers", mm.menu.kind)
	}
	if cmd == nil {
		t.Fatal("expected a background scan/verify command")
	}
	// Drain the (possibly batched) commands so the scan runs.
	var sawSaved bool
	var drain func(c tea.Cmd)
	drain = func(c tea.Cmd) {
		if c == nil {
			return
		}
		msg := c()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, sub := range batch {
				drain(sub)
			}
			return
		}
		if saved, ok := msg.(providerSavedMsg); ok {
			sawSaved = true
			if saved.err != nil {
				t.Fatalf("save/verify failed: %v", saved.err)
			}
		}
	}
	drain(cmd)
	if !sawSaved {
		t.Fatal("no providerSavedMsg emitted")
	}
	mi, ok := caps.Get("deepseek-chat")
	if !ok {
		t.Fatal("deepseek-chat not scanned after saving provider")
	}
	if mi.Provider != "deepseek" || mi.Source != llm.SourceProvider {
		t.Fatalf("model info = %+v", mi)
	}
}

func TestProviderFormInvalidKeyRollsBackAndReopensForm(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key provided","code":"invalid_api_key"}}`))
	}))
	defer srv.Close()

	mgr := providers.NewManager(t.TempDir())
	caps := llm.NewCapabilityRegistry()
	m := New(Options{ProviderMgr: mgr, CapabilityRegistry: caps})
	m.mode = modeMenu
	m.menu = interactiveMenu{
		kind:   menuProviderForm,
		form:   []string{"openai", "openai", srv.URL + "/v1", "bad-key", ""},
		formAt: 4,
	}

	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := out.(Model)
	if mm.menu.kind != menuProviders {
		t.Fatalf("menu kind immediately after submit = %v, want providers while verification runs", mm.menu.kind)
	}
	if cmd == nil {
		t.Fatal("expected background verification command")
	}

	var saved providerSavedMsg
	var found bool
	var drain func(tea.Cmd)
	drain = func(c tea.Cmd) {
		if c == nil {
			return
		}
		msg := c()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, sub := range batch {
				drain(sub)
			}
			return
		}
		if got, ok := msg.(providerSavedMsg); ok {
			saved = got
			found = true
		}
	}
	drain(cmd)
	if !found || saved.err == nil {
		t.Fatalf("providerSavedMsg = %+v, want validation error", saved)
	}
	if !saved.rolledBack || saved.rollbackErr != nil {
		t.Fatalf("rollback state = rolledBack %v, err %v", saved.rolledBack, saved.rollbackErr)
	}
	if names := mgr.Names(); len(names) != 0 {
		t.Fatalf("invalid provider remained configured: %v", names)
	}

	out, _ = mm.Update(saved)
	mm = out.(Model)
	if mm.mode != modeMenu || mm.menu.kind != menuProviderForm {
		t.Fatalf("failed validation did not reopen provider form: mode=%v menu=%v", mm.mode, mm.menu.kind)
	}
	if mm.menu.editName != "" || mm.menu.formAt != 3 {
		t.Fatalf("reopened form state = editName %q, field %d; want new provider API-key field", mm.menu.editName, mm.menu.formAt)
	}
	if len(mm.menu.form) < 4 || mm.menu.form[3] != "bad-key" {
		t.Fatalf("reopened form did not preserve entered values: %#v", mm.menu.form)
	}
	rendered := mm.renderProviderForm()
	if !strings.Contains(rendered, "401") || !strings.Contains(rendered, "Provider was not added") {
		t.Fatalf("validation error is not visible in form: %q", rendered)
	}
}

func TestProviderEditPrefillsMaskedKeyAndPreservesIt(t *testing.T) {
	mgr := providers.NewManager(t.TempDir())
	if err := mgr.Add("router", "openai", "https://router.example/v1", "secret-key", "model"); err != nil {
		t.Fatal(err)
	}
	mgr.Reload()
	m := New(Options{ProviderMgr: mgr, CapabilityRegistry: llm.NewCapabilityRegistry()})
	m.mode = modeMenu
	m.menu = interactiveMenu{kind: menuProviders}

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	mm := out.(Model)
	if mm.menu.kind != menuProviderForm || len(mm.menu.form) < 5 || mm.menu.form[3] != "secret-key" || mm.menu.form[4] != "model" {
		t.Fatalf("edit form did not retain stored key: %+v", mm.menu)
	}
	masked := mm.renderProviderForm()
	if strings.Contains(masked, "secret-key") || !strings.Contains(masked, "**********") {
		t.Fatalf("key should be masked until explicitly revealed: %q", masked)
	}
	mm.menu.formAt = 3
	out, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRight})
	mm = out.(Model)
	if !strings.Contains(mm.renderProviderForm(), "secret-key") {
		t.Fatal("Right Arrow should reveal the prefilled key")
	}

	out, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm = out.(Model)
	if mm.menu.kind != menuProviderForm || mm.menu.formAt != 4 {
		t.Fatalf("API key Enter should advance to optional model field: %+v", mm.menu)
	}
	out, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm = out.(Model)
	if mm.menu.kind != menuProviders {
		t.Fatalf("menu kind = %v, want providers after save", mm.menu.kind)
	}
	mgr.Reload()
	providers := mgr.Configured()
	if len(providers) != 1 || providers[0].APIKey != "secret-key" {
		t.Fatalf("unrelated edit deleted provider key: %+v", providers)
	}
}

// ---------- helpers ---------------------------------------------------------

func init() {
	// Ensure test temp files don't leak.
	os.Setenv("SUPERCLI_DEBUG", "0")
}
