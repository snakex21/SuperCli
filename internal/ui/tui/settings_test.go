package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/system/config"
)

// typeRunes feeds each rune of s through the text-edit key handler, as if
// the user typed it while a text knob was in edit mode.
func typeRunes(m Model, s string) Model {
	for _, r := range s {
		mm, _ := m.settingsEditKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = mm.(Model)
	}
	return m
}

// newSettingsModel builds a Model whose global config.toml lives in a
// temp dir seeded with the given contents, and opens the /settings panel.
func newSettingsModel(t *testing.T, toml string) Model {
	t.Helper()
	dir := t.TempDir()
	if toml != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(toml), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ti := newInputArea()
	p := DefaultPalette()
	m := Model{home: dir, dataDir: dir, palette: p, marker: NewMarker(p), input: ti}
	mm, _ := m.openSettingsMenu()
	return mm.(Model)
}

// cursorForKey positions the menu cursor on the row with the given key.
func cursorForKey(m Model, key string) Model {
	for i, r := range settingsRows() {
		if r.key == key {
			m.menu.cursor = i
			break
		}
	}
	return m
}

func loadCfg(t *testing.T, m Model) config.TomlConfig {
	t.Helper()
	cfg, err := config.LoadToml(m.settingsGlobalPath())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}

// TestSettings_RenderListsKnobs: the panel renders every managed knob,
// the reset-all row, and the next-session marker.
func TestSettings_RenderListsKnobs(t *testing.T) {
	m := newSettingsModel(t, "")
	out := m.renderSettingsMenu()
	for _, want := range []string{
		"Settings", "orchestrator", "thinking", "navigator", "stable_toolset",
		"cache_prompt", "darwin_parallel", "task_parallel", "memory_briefing_tokens",
		"task_max_steps", "task_max_tokens", "default_model", "default_provider",
		"Reset all to defaults", "(next session)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("settings render missing %q\n%s", want, out)
		}
	}
}

// TestSettings_RenderShowsSourceAndDefaults: with an empty config, a
// tri-state with an auto default shows "auto", a fixed-default one shows
// its default, and an int shows the default sentinel.
func TestSettings_RenderShowsSourceAndDefaults(t *testing.T) {
	m := newSettingsModel(t, "")
	out := m.renderSettingsMenu()
	for _, want := range []string{"auto", "default"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in render\n%s", want, out)
		}
	}
}

// TestSettings_ToggleTriStatePersists: Enter on task_parallel cycles it
// off nil → true and writes the key to config.toml.
func TestSettings_ToggleTriStatePersists(t *testing.T) {
	m := newSettingsModel(t, "")
	m = cursorForKey(m, "task_parallel")
	mm, _ := m.settingsEnter()
	m = mm.(Model)
	cfg := loadCfg(t, m)
	if cfg.TaskParallel == nil || !*cfg.TaskParallel {
		t.Fatalf("task_parallel should be true after one toggle, got %v", cfg.TaskParallel)
	}
	// Second toggle → false.
	mm, _ = m.settingsEnter()
	m = mm.(Model)
	cfg = loadCfg(t, m)
	if cfg.TaskParallel == nil || *cfg.TaskParallel {
		t.Fatalf("task_parallel should be false after two toggles, got %v", cfg.TaskParallel)
	}
	// Third toggle → nil (back to auto/default).
	mm, _ = m.settingsEnter()
	m = mm.(Model)
	cfg = loadCfg(t, m)
	if cfg.TaskParallel != nil {
		t.Fatalf("task_parallel should be nil after three toggles, got %v", *cfg.TaskParallel)
	}
}

// TestSettings_NavigatorCycles: navigator cycles "" → on → off → "".
func TestSettings_NavigatorCycles(t *testing.T) {
	m := newSettingsModel(t, "")
	m = cursorForKey(m, "navigator")
	want := []string{"on", "off", ""}
	for _, exp := range want {
		mm, _ := m.settingsEnter()
		m = mm.(Model)
		if got := loadCfg(t, m).Navigator; got != exp {
			t.Fatalf("navigator = %q, want %q", got, exp)
		}
	}
}

// TestSettings_ResetRemovesKey: an explicit orchestrator=false is dropped
// from config.toml on reset (nil pointer → key omitted on save).
func TestSettings_ResetRemovesKey(t *testing.T) {
	m := newSettingsModel(t, "orchestrator = false\n")
	if cfg := loadCfg(t, m); cfg.Orchestrator == nil {
		t.Fatal("precondition: orchestrator should be present")
	}
	m = cursorForKey(m, "orchestrator")
	mm, _ := m.settingsResetCurrent()
	m = mm.(Model)
	if cfg := loadCfg(t, m); cfg.Orchestrator != nil {
		t.Fatalf("orchestrator key should be gone after reset, got %v", *cfg.Orchestrator)
	}
	// And the raw file must no longer carry the line.
	raw, _ := os.ReadFile(m.settingsGlobalPath())
	if strings.Contains(string(raw), "orchestrator") {
		t.Errorf("config.toml still mentions orchestrator:\n%s", raw)
	}
}

// TestSettings_EditIntPersists: editing memory_briefing_tokens writes the
// typed value.
func TestSettings_EditIntPersists(t *testing.T) {
	m := newSettingsModel(t, "")
	m = cursorForKey(m, "memory_briefing_tokens")
	mm, _ := m.settingsEnter() // enters editing mode
	m = mm.(Model)
	if !m.menu.editing {
		t.Fatal("expected editing mode after Enter on an int row")
	}
	m.menu.editBuf = "250"
	mm, _ = m.settingsCommitInt()
	m = mm.(Model)
	if cfg := loadCfg(t, m); cfg.MemoryBriefingTokens != 250 {
		t.Fatalf("memory_briefing_tokens = %d, want 250", cfg.MemoryBriefingTokens)
	}
}

// TestSettings_EditTextPersists: Enter on task_model enters edit mode,
// typed characters build the value, and Enter writes it to config.toml.
func TestSettings_EditTextPersists(t *testing.T) {
	m := newSettingsModel(t, "")
	m = cursorForKey(m, "task_model")
	mm, _ := m.settingsEnter() // enter edit mode
	m = mm.(Model)
	if !m.menu.editing {
		t.Fatal("expected editing mode after Enter on a text row")
	}
	if m.menu.editBuf != "" {
		t.Fatalf("editBuf should preload empty for unset task_model, got %q", m.menu.editBuf)
	}
	m = typeRunes(m, "small/ministral-3b")
	// Enter commits.
	mm, _ = m.settingsEditKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if m.menu.editing {
		t.Fatal("editing should end after Enter")
	}
	if cfg := loadCfg(t, m); cfg.TaskModel != "small/ministral-3b" {
		t.Fatalf("task_model = %q, want small/ministral-3b", cfg.TaskModel)
	}
}

// TestSettings_EditTextBackspace: backspace deletes a trailing rune from
// the edit buffer before commit.
func TestSettings_EditTextBackspace(t *testing.T) {
	m := newSettingsModel(t, "")
	m = cursorForKey(m, "task_model")
	mm, _ := m.settingsEnter()
	m = mm.(Model)
	m = typeRunes(m, "abcX")
	mm, _ = m.settingsEditKey(tea.KeyMsg{Type: tea.KeyBackspace})
	m = mm.(Model)
	if m.menu.editBuf != "abc" {
		t.Fatalf("editBuf after backspace = %q, want abc", m.menu.editBuf)
	}
	mm, _ = m.settingsEditKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if cfg := loadCfg(t, m); cfg.TaskModel != "abc" {
		t.Fatalf("task_model = %q, want abc", cfg.TaskModel)
	}
}

// TestSettings_EditTextEscCancels: Esc leaves the underlying value
// untouched even after typing.
func TestSettings_EditTextEscCancels(t *testing.T) {
	m := newSettingsModel(t, "task_model = \"keep-me\"\n")
	m = cursorForKey(m, "task_model")
	mm, _ := m.settingsEnter()
	m = mm.(Model)
	if m.menu.editBuf != "keep-me" {
		t.Fatalf("editBuf should preload existing value, got %q", m.menu.editBuf)
	}
	m = typeRunes(m, "-junk")
	mm, _ = m.settingsEditKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = mm.(Model)
	if m.menu.editing {
		t.Fatal("editing should end after Esc")
	}
	if cfg := loadCfg(t, m); cfg.TaskModel != "keep-me" {
		t.Fatalf("task_model = %q, want keep-me (Esc must not persist)", cfg.TaskModel)
	}
}

// TestSettings_EditTextEmptyClears: committing an empty buffer clears the
// knob and drops the key from config.toml.
func TestSettings_EditTextEmptyClears(t *testing.T) {
	m := newSettingsModel(t, "task_model = \"small/x\"\n")
	m = cursorForKey(m, "task_model")
	mm, _ := m.settingsEnter()
	m = mm.(Model)
	// Erase the preloaded value.
	m.menu.editBuf = "   " // whitespace-only → treated as empty
	mm, _ = m.settingsEditKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if cfg := loadCfg(t, m); cfg.TaskModel != "" {
		t.Fatalf("task_model should be cleared, got %q", cfg.TaskModel)
	}
	// Non-pointer string keys serialize as an empty value rather than
	// being dropped (same as the int zero-sentinel), so the file may
	// still carry `task_model = ""` — the important guarantee is that no
	// stale non-empty value survives.
	raw, _ := os.ReadFile(m.settingsGlobalPath())
	if strings.Contains(string(raw), "small/x") {
		t.Errorf("config.toml still carries the old task_model value:\n%s", raw)
	}
}

// TestSettings_EditVerifyCommandsList: verify_commands is edited as a
// single semicolon-separated field and parsed back into a trimmed list.
func TestSettings_EditVerifyCommandsList(t *testing.T) {
	m := newSettingsModel(t, "")
	m = cursorForKey(m, "verify_commands")
	mm, _ := m.settingsEnter()
	m = mm.(Model)
	m.menu.editBuf = "go build ./... ;  go test ./... ; "
	mm, _ = m.settingsEditKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	want := []string{"go build ./...", "go test ./..."}
	if cfg := loadCfg(t, m); !reflect.DeepEqual(cfg.VerifyCommands, want) {
		t.Fatalf("verify_commands = %v, want %v", cfg.VerifyCommands, want)
	}
}

// TestSettings_EditVerifyCommandsPreloadsJoined: entering edit mode on an
// existing list preloads the semicolon-joined form for round-tripping.
func TestSettings_EditVerifyCommandsPreloadsJoined(t *testing.T) {
	m := newSettingsModel(t, "verify_commands = [\"go build ./...\", \"go vet ./...\"]\n")
	m = cursorForKey(m, "verify_commands")
	mm, _ := m.settingsEnter()
	m = mm.(Model)
	if m.menu.editBuf != "go build ./... ; go vet ./..." {
		t.Fatalf("editBuf preload = %q", m.menu.editBuf)
	}
}

// TestSettings_ResetAllKeepsProviders is the critical guard: resetting all
// managed knobs must NOT touch provider entries or API keys.
func TestSettings_ResetAllKeepsProviders(t *testing.T) {
	seed := `default_model = "deepseek-v4-flash"
default_provider = "deepseek"
orchestrator = true
navigator = "on"
task_max_steps = 7
memory_briefing_tokens = 250
hidden_models = ["gpt-4o"]

[[providers]]
name = "deepseek"
type = "openai"
base_url = "https://api.deepseek.com/v1"
api_key = "sk-secret-key-123"
model = "deepseek-v4-flash"
`
	m := newSettingsModel(t, seed)
	// Position on the reset-all row and confirm it.
	m = cursorForKey(m, "")
	mm, _ := m.settingsEnter()
	m = mm.(Model)

	cfg := loadCfg(t, m)
	// Managed knobs cleared.
	if cfg.Orchestrator != nil {
		t.Errorf("orchestrator not reset: %v", *cfg.Orchestrator)
	}
	if cfg.Navigator != "" {
		t.Errorf("navigator not reset: %q", cfg.Navigator)
	}
	if cfg.TaskMaxSteps != 0 {
		t.Errorf("task_max_steps not reset: %d", cfg.TaskMaxSteps)
	}
	if cfg.MemoryBriefingTokens != 0 {
		t.Errorf("memory_briefing_tokens not reset: %d", cfg.MemoryBriefingTokens)
	}
	// Providers + API keys + hidden models UNTOUCHED — this is the
	// whole point: a settings reset must not wipe provider config.
	if len(cfg.Providers) != 1 {
		t.Fatalf("providers wiped: got %d, want 1", len(cfg.Providers))
	}
	if cfg.Providers[0].APIKey != "sk-secret-key-123" {
		t.Errorf("API key lost: %q", cfg.Providers[0].APIKey)
	}
	if cfg.Providers[0].BaseURL != "https://api.deepseek.com/v1" {
		t.Errorf("provider base_url lost: %q", cfg.Providers[0].BaseURL)
	}
	if len(cfg.HiddenModels) != 1 {
		t.Errorf("hidden_models lost: %v", cfg.HiddenModels)
	}
	// default_model / default_provider are read-only info rows, not
	// reset — they still identify the startup selection.
	if cfg.DefaultModel != "deepseek-v4-flash" {
		t.Errorf("default_model changed: %q", cfg.DefaultModel)
	}
}
