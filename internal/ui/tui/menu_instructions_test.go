package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"supercli/internal/llm/prompt"
)

func instructionsTestModel(t *testing.T) Model {
	t.Helper()
	m := New(Options{Home: t.TempDir(), DataDir: t.TempDir()})
	m.language = "en"
	m.width, m.height = 100, 32
	return m
}

func instructionsKey(m Model, key tea.KeyMsg) Model {
	next, _ := m.Update(key)
	return next.(Model)
}

func instructionsType(m Model, text string) Model {
	return instructionsKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)})
}

func instructionsOpen(m Model) Model {
	next, _ := m.dispatchSlashCommand(SlashCommand{Name: "instructions"})
	return next.(Model)
}

func instructionsCreate(m Model, name, content string) Model {
	m = instructionsType(m, "n")
	m = instructionsType(m, name)
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyTab})
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(content), Paste: true})
	return instructionsKey(m, tea.KeyMsg{Type: tea.KeyCtrlS})
}

func instructionsLoad(t *testing.T, m Model) prompt.UserInstructionsState {
	t.Helper()
	state, err := prompt.LoadUserInstructions(m.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestInstructionsDiscoverableAndBackNavigation(t *testing.T) {
	m := instructionsTestModel(t)
	found := false
	for _, entry := range HelpContentEntries() {
		if entry.Name == "instructions" {
			found = true
		}
	}
	if !found {
		t.Fatal("/instructions missing from completion/help entries")
	}
	next, _ := m.openActionsMenu()
	m = next.(Model)
	for i, row := range m.filteredActionRows() {
		if row.id == "instructions" {
			m.menu.cursor = i
			found = false
			break
		}
	}
	if found {
		t.Fatal("instructions missing from action centre")
	}
	m.input.SetValue("unsent chat draft")
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.menu.kind != menuInstructions || m.menu.parent == nil || m.menu.parent.kind != menuActions {
		t.Fatal("action did not open instructions")
	}
	m = instructionsType(m, "n")
	m = instructionsType(m, "draft")
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.menu.kind != menuInstructions {
		t.Fatal("cancel did not return to list")
	}
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.menu.kind != menuActions {
		t.Fatal("back did not return to actions")
	}
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode == modeMenu || m.input.Value() != "unsent chat draft" {
		t.Fatal("back lost chat draft")
	}
	if _, err := os.Stat(prompt.UserInstructionsPath(m.dataDir)); !os.IsNotExist(err) {
		t.Fatal("cancel wrote a preset")
	}
	next, _ = m.dispatchSlashCommand(SlashCommand{Name: "prompts"})
	if next.(Model).menu.kind != menuInstructions {
		t.Fatal("/prompts alias failed")
	}
}

func TestInstructionsCreateEditSelectTogglePersistence(t *testing.T) {
	m := instructionsOpen(instructionsTestModel(t))
	// Space cannot enable an empty store, and navigation must never go negative.
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyDown})
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeySpace})
	if m.menu.cursor != 0 || m.menu.formErr == "" || m.menu.instructions.Enabled {
		t.Fatal("empty state not protected")
	}
	content := "Pierwszy wiersz\n\tZażółć gęślą jaźń.\n" + strings.Repeat("long content 日本語\n", 800)
	m = instructionsCreate(m, "Personal", content)
	if m.menu.kind != menuInstructions || m.menu.formErr != "" {
		t.Fatalf("create failed: %s", m.menu.formErr)
	}
	state := instructionsLoad(t, m)
	if len(state.Presets) != 1 || state.Presets[0].Content != content || state.Enabled || state.ActiveID != state.Presets[0].ID {
		t.Fatalf("bad created state: %+v", state)
	}
	firstID := state.ActiveID
	m = instructionsCreate(m, "Work", "Use concise explanations.")
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeySpace})
	state = instructionsLoad(t, m)
	if !state.Enabled || state.ActiveID != state.Presets[1].ID || state.ActiveID == firstID {
		t.Fatal("select/toggle not persisted")
	}
	if got := prompt.ActiveUserInstructions(m.dataDir); !strings.Contains(got, "Work") || !strings.Contains(got, "Use concise explanations.") {
		t.Fatalf("shared active block: %q", got)
	}
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyUp})
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	m = instructionsType(m, "e")
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyCtrlU})
	m = instructionsType(m, "Renamed")
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyTab})
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	m = instructionsType(m, "Additional line")
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	state = instructionsLoad(t, m)
	if state.Presets[0].ID != firstID || state.Presets[0].Name != "Renamed" || state.Presets[0].Content != content+"\nAdditional line" || !state.Enabled {
		t.Fatal("edit changed ID/content/enable state incorrectly")
	}
	if m.menu.cursor != 0 {
		t.Fatal("edited row not retained")
	}
	m = instructionsType(m, "e")
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyEsc})
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyDown})
	m = instructionsType(m, "e")
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.menu.cursor != 1 {
		t.Fatal("cancel reset selected row")
	}
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyUp})
	m = instructionsType(m, "t")
	if instructionsLoad(t, m).Enabled || prompt.ActiveUserInstructions(m.dataDir) != "" {
		t.Fatal("disable failed")
	}
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyEsc})
	m = instructionsOpen(m)
	if !reflect.DeepEqual(m.menu.instructions, instructionsLoad(t, m)) {
		t.Fatal("reopen state differs from disk")
	}
}

func TestInstructionsValidationAndEditCancel(t *testing.T) {
	m := instructionsOpen(instructionsTestModel(t))
	m = instructionsType(m, "n")
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if m.menu.formErr != m.tr("tui.instructions.name_required") {
		t.Fatal("unnamed preset accepted")
	}
	m = instructionsType(m, "Name")
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if m.menu.formErr != m.tr("tui.instructions.content_required") {
		t.Fatal("empty content accepted")
	}
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyTab})
	m = instructionsType(m, " \n\t ")
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if m.menu.formErr == "" {
		t.Fatal("whitespace content accepted")
	}
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyEsc})
	m = instructionsCreate(m, "Original", "original content")
	before, err := os.ReadFile(prompt.UserInstructionsPath(m.dataDir))
	if err != nil {
		t.Fatal(err)
	}
	m = instructionsType(m, "e")
	m = instructionsType(m, "modified")
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyTab})
	m = instructionsType(m, "do not persist")
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyEsc})
	after, _ := os.ReadFile(prompt.UserInstructionsPath(m.dataDir))
	if string(before) != string(after) || m.menu.instructions.Presets[0].Name != "Original" {
		t.Fatal("cancel leaked edits")
	}
}

func TestInstructionsSharedGUIFormatAndUnboundedContent(t *testing.T) {
	m := instructionsTestModel(t)
	// Deliberately exceed both the usual 4K text limit and textarea's 10K-line limit.
	content := "\r\n\t" + strings.Repeat("Polski 日本語\n", 10050) + "tail\n"
	state := prompt.UserInstructionsState{Version: 1, Enabled: true, ActiveID: "gui-id", Presets: []prompt.UserInstructionPreset{{ID: "gui-id", Name: "GUI preset", Content: content}}}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prompt.UserInstructionsPath(m.dataDir), data, 0600); err != nil {
		t.Fatal(err)
	}
	m = instructionsOpen(m)
	m = instructionsType(m, "e")
	if m.menu.instructionEditor.content.value() != content {
		t.Fatal("GUI content truncated on load")
	}
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyTab})
	m = instructionsType(m, "suffix")
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	got := instructionsLoad(t, m)
	if got.Presets[0].Content != content+"suffix" || got.ActiveID != "gui-id" || !got.Enabled {
		t.Fatal("GUI format/content not preserved")
	}
	var raw map[string]json.RawMessage
	data, _ = os.ReadFile(prompt.UserInstructionsPath(m.dataDir))
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"version", "enabled", "active_id", "presets"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("missing shared JSON field %q", key)
		}
	}
}

func TestInstructionsSaveFailureRetainsDraftAndState(t *testing.T) {
	m := instructionsOpen(instructionsTestModel(t))
	m = instructionsCreate(m, "Original", "content")
	m = instructionsCreate(m, "Second", "other content")
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyUp})
	before := instructionsLoad(t, m)
	// Make the store's fixed temporary path unwritable without OS-specific chmod.
	tmp := prompt.UserInstructionsPath(m.dataDir) + ".tmp"
	if err := os.Mkdir(tmp, 0700); err != nil {
		t.Fatal(err)
	}
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeySpace})
	if m.menu.formErr == "" || !reflect.DeepEqual(before, m.menu.instructions) || !reflect.DeepEqual(before, instructionsLoad(t, m)) {
		t.Fatal("failed toggle changed state or hid error")
	}
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyDown})
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.menu.formErr == "" || !reflect.DeepEqual(before, m.menu.instructions) || !reflect.DeepEqual(before, instructionsLoad(t, m)) {
		t.Fatal("failed active selection changed state")
	}
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyUp})
	m = instructionsType(m, "e")
	m = instructionsType(m, " renamed")
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if m.menu.kind != menuInstructionEditor || m.menu.formErr == "" || m.menu.instructionEditor.name.value() != "Original renamed" {
		t.Fatal("failed save discarded editor")
	}
	if !reflect.DeepEqual(before, m.menu.instructions) || !reflect.DeepEqual(before, instructionsLoad(t, m)) {
		t.Fatal("failed edit mutated saved state")
	}
	if err := os.Remove(tmp); err != nil {
		t.Fatal(err)
	}
	m = instructionsKey(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if m.menu.kind != menuInstructions || instructionsLoad(t, m).Presets[0].Name != "Original renamed" {
		t.Fatal("retry did not save retained draft")
	}
}

func TestInstructionsLoadFailureCannotOverwriteStore(t *testing.T) {
	m := instructionsTestModel(t)
	path := prompt.UserInstructionsPath(m.dataDir)
	bad := []byte("{broken json")
	if err := os.WriteFile(path, bad, 0600); err != nil {
		t.Fatal(err)
	}
	m = instructionsOpen(m)
	if m.menu.formErr == "" || m.menu.instructionsReady {
		t.Fatal("load error not visible")
	}
	for _, key := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("n")}, {Type: tea.KeySpace}, {Type: tea.KeyEnter}} {
		m = instructionsKey(m, key)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(bad) || m.menu.kind != menuInstructions {
		t.Fatal("load failure allowed overwrite")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	m = instructionsType(m, "r")
	if !m.menu.instructionsReady || m.menu.formErr != "" {
		t.Fatal("reload did not recover")
	}
	// Also exercise an invalid data directory without accessing real userdata.
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	m.dataDir = file
	m = instructionsOpen(m)
	// Windows may classify a file used as a parent directory as not-exist on
	// load. Saving must still report the error without losing the draft.
	if m.menu.instructionsReady {
		m = instructionsCreate(m, "Name", "content")
	}
	if m.menu.formErr == "" {
		t.Fatal("invalid data directory not reported")
	}
}

func TestInstructionsLocalizationAndEditorGeometry(t *testing.T) {
	for _, language := range []string{"en", "pl", "de", "unsupported"} {
		m := instructionsOpen(instructionsTestModel(t))
		m.language = language
		if m.tr("tui.instructions.title") == "tui.instructions.title" {
			t.Fatal("missing translation/fallback")
		}
		m = instructionsCreate(m, "Name", strings.Repeat("wide 日本語\n", 100))
		list := ansi.Strip(m.renderMenuView())
		if !strings.Contains(list, m.tr("tui.instructions.enabled")) && !strings.Contains(list, m.tr("tui.instructions.disabled")) {
			t.Fatal("enable state hidden")
		}
		m = instructionsType(m, "e")
		for _, size := range [][2]int{{100, 32}, {60, 24}, {40, 20}, {24, 14}} {
			m.width, m.height = size[0], size[1]
			view := ansi.Strip(m.renderMenuView())
			if len(strings.Split(view, "\n")) > m.height {
				t.Fatalf("editor exceeds height at %v", size)
			}
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > m.width {
					t.Fatalf("editor exceeds width at %v: %q", size, line)
				}
			}
		}
	}
}

func TestInstructionBufferNavigationLosslessAndSafeView(t *testing.T) {
	b := newInstructionBuffer("abc\n日本語\nlast")
	key := func(k tea.KeyType) { b.update(tea.KeyMsg{Type: k}, true, 80, 10) }
	key(tea.KeyCtrlHome)
	key(tea.KeyDown)
	key(tea.KeyRight)
	key(tea.KeyDelete)
	b.insert("X")
	if b.value() != "abc\n日X語\nlast" {
		t.Fatalf("editing: %q", b.value())
	}
	key(tea.KeyUp)
	key(tea.KeyHome)
	key(tea.KeyEnd)
	key(tea.KeyEnter)
	key(tea.KeyBackspace)
	if b.value() != "abc\n日X語\nlast" {
		t.Fatal("newline/backspace did not round-trip")
	}
	key(tea.KeyCtrlEnd)
	b.insert("\x1b[2J\t\r")
	view := b.view(10, 3, true, Palette{})
	if strings.Contains(view, "\x1b[2J") {
		t.Fatal("content injected a terminal control sequence")
	}
	if !strings.HasSuffix(b.value(), "\x1b[2J\t\r") {
		t.Fatal("view sanitized stored content")
	}
	long := newInstructionBuffer(strings.Repeat("x", 5000) + "TAIL")
	if !strings.Contains(ansi.Strip(long.view(20, 3, true, Palette{})), "TAIL") {
		t.Fatal("long-line caret not scrolled into view")
	}
}
