package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// --- splitAutocompleteTrigger tests ---

func TestSplitAutocompleteTrigger_SlashAtStart(t *testing.T) {
	kind, query := splitAutocompleteTrigger("/models")
	if kind != autocompSlash {
		t.Fatalf("expected autocompSlash, got %d", kind)
	}
	if query != "models" {
		t.Fatalf("expected 'models', got %q", query)
	}
}

func TestSplitAutocompleteTrigger_SlashEmpty(t *testing.T) {
	kind, query := splitAutocompleteTrigger("/")
	if kind != autocompSlash {
		t.Fatalf("expected autocompSlash, got %d", kind)
	}
	if query != "" {
		t.Fatalf("expected empty query, got %q", query)
	}
}

func TestSplitAutocompleteTrigger_SlashPartial(t *testing.T) {
	kind, query := splitAutocompleteTrigger("/mo")
	if kind != autocompSlash {
		t.Fatalf("expected autocompSlash, got %d", kind)
	}
	if query != "mo" {
		t.Fatalf("expected 'mo', got %q", query)
	}
}

func TestSplitAutocompleteTrigger_MentionAtStart(t *testing.T) {
	kind, query := splitAutocompleteTrigger("@main.go")
	if kind != autocompMention {
		t.Fatalf("expected autocompMention, got %d", kind)
	}
	if query != "main.go" {
		t.Fatalf("expected 'main.go', got %q", query)
	}
}

func TestSplitAutocompleteTrigger_MentionAfterSpace(t *testing.T) {
	kind, query := splitAutocompleteTrigger("look at @file.txt")
	if kind != autocompMention {
		t.Fatalf("expected autocompMention, got %d", kind)
	}
	if query != "file.txt" {
		t.Fatalf("expected 'file.txt', got %q", query)
	}
}

func TestSplitAutocompleteTrigger_MentionAfterTab(t *testing.T) {
	kind, query := splitAutocompleteTrigger("look\t@foo.go")
	if kind != autocompMention {
		t.Fatalf("expected autocompMention, got %d", kind)
	}
	if query != "foo.go" {
		t.Fatalf("expected 'foo.go', got %q", query)
	}
}

func TestSplitAutocompleteTrigger_NoTrigger(t *testing.T) {
	kind, _ := splitAutocompleteTrigger("hello world")
	if kind != autocompNone {
		t.Fatalf("expected autocompNone, got %d", kind)
	}
}

func TestSplitAutocompleteTrigger_Empty(t *testing.T) {
	kind, _ := splitAutocompleteTrigger("")
	if kind != autocompNone {
		t.Fatalf("expected autocompNone, got %d", kind)
	}
}

func TestSplitAutocompleteTrigger_SlashAfterSpace(t *testing.T) {
	// "/" only triggers at position 0, not after space.
	kind, _ := splitAutocompleteTrigger("foo /bar")
	if kind != autocompNone {
		t.Fatalf("expected autocompNone for mid-text slash, got %d", kind)
	}
}

func TestSplitAutocompleteTrigger_MentionMidWord(t *testing.T) {
	// @ not preceded by space/tab/newline is not a trigger.
	kind, _ := splitAutocompleteTrigger("foo@bar")
	if kind != autocompNone {
		t.Fatalf("expected autocompNone for mid-word @, got %d", kind)
	}
}

// --- filterItems tests ---

func TestFilterItems_EmptyQuery(t *testing.T) {
	items := []autocompleteItem{
		{Label: "/help", Desc: "help"},
		{Label: "/goal", Desc: "goal"},
	}
	filtered := filterItems(items, "")
	if len(filtered) != 2 {
		t.Fatalf("expected 2 items, got %d", len(filtered))
	}
}

func TestFilterItems_Match(t *testing.T) {
	items := []autocompleteItem{
		{Label: "/models", Desc: "list models"},
		{Label: "/model", Desc: "swap model"},
		{Label: "/goal", Desc: "manage goal"},
	}
	filtered := filterItems(items, "mod")
	if len(filtered) != 2 {
		t.Fatalf("expected 2 items, got %d", len(filtered))
	}
	if filtered[0].Label != "/models" {
		t.Fatalf("expected /models first, got %q", filtered[0].Label)
	}
	if filtered[1].Label != "/model" {
		t.Fatalf("expected /model second, got %q", filtered[1].Label)
	}
}

func TestFilterItems_NoMatch(t *testing.T) {
	items := []autocompleteItem{
		{Label: "/help", Desc: "help"},
	}
	filtered := filterItems(items, "xyz")
	if len(filtered) != 0 {
		t.Fatalf("expected 0 items, got %d", len(filtered))
	}
}

func TestFilterItems_CaseInsensitive(t *testing.T) {
	items := []autocompleteItem{
		{Label: "/Goal", Desc: "goal"},
	}
	filtered := filterItems(items, "GOAL")
	if len(filtered) != 1 {
		t.Fatalf("expected 1 item, got %d", len(filtered))
	}
}

// --- buildSlashItems tests ---

func TestBuildSlashItems_EmptyCommands(t *testing.T) {
	// Even with nil commands, all HelpContentEntries should appear
	// because many commands are handled inline in dispatchSlashCommand.
	items := buildSlashItems(nil)
	if len(items) == 0 {
		t.Fatal("expected items even with nil commands map")
	}
}

func TestBuildSlashItems_WithCommands(t *testing.T) {
	commands := map[string]SlashHandler{
		"help":   func(_ context.Context, _ string) (string, error) { return "", nil },
		"goal":   func(_ context.Context, _ string) (string, error) { return "", nil },
		"models": func(_ context.Context, _ string) (string, error) { return "", nil },
		"quit":   func(_ context.Context, _ string) (string, error) { return "", nil },
	}
	items := buildSlashItems(commands)
	// Should include ALL HelpContentEntries, not just those in commands map.
	if len(items) < 15 {
		t.Fatalf("expected at least 15 items (all help entries), got %d", len(items))
	}
	// Check that inline-handled commands are included.
	labels := make(map[string]bool)
	for _, it := range items {
		labels[it.Label] = true
		if !strings.HasPrefix(it.Label, "/") {
			t.Fatalf("expected label to start with /, got %q", it.Label)
		}
		if !strings.HasSuffix(it.Value, " ") {
			t.Fatalf("expected value to end with space, got %q", it.Value)
		}
	}
	for _, name := range []string{"/model", "/providers", "/goal", "/plan", "/export", "/quit", "/help"} {
		if !labels[name] {
			t.Fatalf("expected %q in autocomplete items", name)
		}
	}
}

// --- renderAutocomplete tests ---

func TestRenderAutocomplete_Nil(t *testing.T) {
	result := renderAutocomplete(nil, 80, DefaultPalette())
	if result != "" {
		t.Fatalf("expected empty string for nil, got %q", result)
	}
}

func TestRenderAutocomplete_Empty(t *testing.T) {
	result := renderAutocomplete(&autocomplete{}, 80, DefaultPalette())
	if result != "" {
		t.Fatalf("expected empty string for empty autocomplete, got %q", result)
	}
}

func TestRenderAutocomplete_WithItems(t *testing.T) {
	ac := &autocomplete{
		kind: autocompSlash,
		items: []autocompleteItem{
			{Label: "/help", Desc: "show help", Value: "/help "},
			{Label: "/goal", Desc: "manage goal", Value: "/goal "},
		},
		cursor: 0,
	}
	result := renderAutocomplete(ac, 80, DefaultPalette())
	if result == "" {
		t.Fatal("expected non-empty render output")
	}
	if !strings.Contains(result, "/help") {
		t.Fatalf("expected /help in output, got %q", result)
	}
	if !strings.Contains(result, "/goal") {
		t.Fatalf("expected /goal in output, got %q", result)
	}
}

func TestRenderAutocomplete_FilteredEmpty(t *testing.T) {
	ac := &autocomplete{
		kind:  autocompSlash,
		items: []autocompleteItem{{Label: "/help", Desc: "help"}},
		query: "zzz",
	}
	result := renderAutocomplete(ac, 80, DefaultPalette())
	if result != "" {
		t.Fatalf("expected empty for no matches, got %q", result)
	}
}

func TestRenderAutocomplete_ManyItems(t *testing.T) {
	items := make([]autocompleteItem, 20)
	for i := range items {
		items[i] = autocompleteItem{
			Label: "/cmd" + string(rune('a'+i%26)),
			Desc:  "desc",
		}
	}
	ac := &autocomplete{
		kind:   autocompSlash,
		items:  items,
		cursor: 10,
	}
	result := renderAutocomplete(ac, 80, DefaultPalette())
	if result == "" {
		t.Fatal("expected non-empty output")
	}
}

// --- buildMentionItems tests ---

func TestBuildMentionItems(t *testing.T) {
	// Create a temp directory with some files.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "hello.go"), "package main")
	writeFile(t, filepath.Join(dir, ".hidden"), "hidden")
	writeFile(t, filepath.Join(dir, "readme.txt"), "readme")
	if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	items := buildMentionItems(dir)
	if len(items) != 3 { // hello.go, readme.txt, subdir/
		t.Fatalf("expected 3 items (skip .hidden), got %d: %v", len(items), items)
	}

	// Check that subdir has trailing slash.
	found := false
	for _, it := range items {
		if strings.HasSuffix(it.Label, "/") {
			found = true
			if it.Desc != "dir" {
				t.Fatalf("expected 'dir' desc for directory, got %q", it.Desc)
			}
		}
	}
	if !found {
		t.Fatal("expected at least one directory in items")
	}
}

// --- humanSize tests ---

func TestHumanSize(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0B"},
		{512, "512B"},
		{1024, "1.0KB"},
		{1536, "1.5KB"},
		{1048576, "1.0MB"},
		{2621440, "2.5MB"},
	}
	for _, tt := range tests {
		got := humanSize(tt.bytes)
		if got != tt.want {
			t.Errorf("humanSize(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}

// --- autocomplete navigation simulation tests ---

func TestAutocomplete_OpenAndClose(t *testing.T) {
	m := newTestModel(t)
	m.commands = map[string]SlashHandler{
		"help":   func(_ context.Context, _ string) (string, error) { return "help text", nil },
		"goal":   func(_ context.Context, _ string) (string, error) { return "", nil },
		"models": func(_ context.Context, _ string) (string, error) { return "", nil },
		"quit":   func(_ context.Context, _ string) (string, error) { return "", nil },
	}

	// Type "/" to open autocomplete.
	m.input.SetValue("/")
	m.updateAutocompleteState()

	if m.autocomp.kind != autocompSlash {
		t.Fatalf("expected slash autocomplete to open, got kind=%d", m.autocomp.kind)
	}
	if len(m.autocomp.items) == 0 {
		t.Fatal("expected items in autocomplete")
	}

	// Press ESC to close.
	escMsg := tea.KeyMsg{Type: tea.KeyEscape}
	m2, _ := m.handleAutocompleteKey(escMsg)
	m3 := m2.(Model)
	if m3.autocomp.kind != autocompNone {
		t.Fatalf("expected autocomplete to close after ESC, got kind=%d", m3.autocomp.kind)
	}
}

func TestAutocomplete_FilterSlash(t *testing.T) {
	m := newTestModel(t)
	m.commands = map[string]SlashHandler{
		"help":   func(_ context.Context, _ string) (string, error) { return "", nil },
		"goal":   func(_ context.Context, _ string) (string, error) { return "", nil },
		"model":  func(_ context.Context, _ string) (string, error) { return "", nil },
		"quit":   func(_ context.Context, _ string) (string, error) { return "", nil },
	}

	m.input.SetValue("/mo")
	m.updateAutocompleteState()

	if m.autocomp.kind != autocompSlash {
		t.Fatalf("expected slash autocomplete, got kind=%d", m.autocomp.kind)
	}
	filtered := filterItems(m.autocomp.items, m.autocomp.query)
	if len(filtered) != 1 {
		t.Fatalf("expected 1 filtered item (/model), got %d", len(filtered))
	}
}

func TestAutocomplete_NavigateUpDown(t *testing.T) {
	m := newTestModel(t)
	m.commands = map[string]SlashHandler{
		"help": func(_ context.Context, _ string) (string, error) { return "", nil },
		"goal": func(_ context.Context, _ string) (string, error) { return "", nil },
		"quit": func(_ context.Context, _ string) (string, error) { return "", nil },
	}
	m.input.SetValue("/")
	m.updateAutocompleteState()

	startCursor := m.autocomp.cursor

	// Press down.
	downMsg := tea.KeyMsg{Type: tea.KeyDown}
	m2, _ := m.handleAutocompleteKey(downMsg)
	m3 := m2.(Model)
	if m3.autocomp.cursor <= startCursor {
		t.Fatalf("expected cursor to move down: %d -> %d", startCursor, m3.autocomp.cursor)
	}

	// Press up.
	upMsg := tea.KeyMsg{Type: tea.KeyUp}
	m4, _ := m3.handleAutocompleteKey(upMsg)
	m5 := m4.(Model)
	if m5.autocomp.cursor != startCursor {
		t.Fatalf("expected cursor back at %d, got %d", startCursor, m5.autocomp.cursor)
	}
}

func TestAutocomplete_AcceptWithTab(t *testing.T) {
	m := newTestModel(t)
	m.commands = map[string]SlashHandler{
		"help": func(_ context.Context, _ string) (string, error) { return "", nil },
		"quit": func(_ context.Context, _ string) (string, error) { return "", nil },
	}
	m.input.SetValue("/")
	m.updateAutocompleteState()
	m.autocomp.cursor = 0

	// Press Tab to accept (fill only, no execute).
	tabMsg := tea.KeyMsg{Type: tea.KeyTab}
	m2, _ := m.handleAutocompleteKey(tabMsg)
	m3 := m2.(Model)

	if m3.autocomp.kind != autocompNone {
		t.Fatalf("expected autocomplete to close after Tab, got kind=%d", m3.autocomp.kind)
	}
	val := m3.input.Value()
	if val == "" || val == "/" {
		t.Fatalf("expected input to be filled after Tab, got %q", val)
	}
	// Tab should NOT have dispatched — input still has value.
	if !strings.HasPrefix(val, "/") {
		t.Fatalf("expected command prefix after Tab, got %q", val)
	}
}

func TestAutocomplete_AcceptWithEnterExecutesImmediately(t *testing.T) {
	m := newTestModel(t)
	// Register a handler that returns a known result.
	m.commands = map[string]SlashHandler{
		"help": func(_ context.Context, _ string) (string, error) { return "HELP_TEXT", nil },
	}
	m.input.SetValue("/")
	m.updateAutocompleteState()
	m.autocomp.cursor = 0

	// Press Enter to accept + execute.
	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	m2, cmd := m.handleAutocompleteKey(enterMsg)
	m3 := m2.(Model)

	// Autocomplete should be closed.
	if m3.autocomp.kind != autocompNone {
		t.Fatalf("expected autocomplete to close after Enter, got kind=%d", m3.autocomp.kind)
	}

	// The returned command should be non-nil (dispatches the slash command).
	if cmd == nil {
		t.Fatal("expected non-nil tea.Cmd from Enter (should dispatch slash command)")
	}

	// Execute the command and check the result.
	msg := cmd()
	if sr, ok := msg.(slashResultMsg); ok {
		if sr.Body != "HELP_TEXT" {
			t.Fatalf("expected HELP_TEXT, got %q", sr.Body)
		}
	} else {
		t.Fatalf("expected slashResultMsg, got %T: %v", msg, msg)
	}
}

func TestAutocomplete_ClosesWhenTriggerRemoved(t *testing.T) {
	m := newTestModel(t)
	m.commands = map[string]SlashHandler{
		"help": func(_ context.Context, _ string) (string, error) { return "", nil },
	}
	m.input.SetValue("/")
	m.updateAutocompleteState()

	if m.autocomp.kind != autocompSlash {
		t.Fatal("expected autocomplete to be open")
	}

	// Type a non-trigger character sequence — clear the slash.
	m.input.SetValue("x")
	keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}}
	// We need to simulate what handleKey does: update textinput, then updateAutocompleteState.
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(keyMsg)
	_ = cmd
	m.updateAutocompleteState()

	if m.autocomp.kind != autocompNone {
		t.Fatalf("expected autocomplete to close when trigger removed, got kind=%d", m.autocomp.kind)
	}
}

func TestAutocomplete_MentionOpensWithAt(t *testing.T) {
	m := newTestModel(t)
	m.home = t.TempDir()
	writeFile(t, filepath.Join(m.home, "test.go"), "package main")

	m.input.SetValue("@te")
	m.updateAutocompleteState()

	if m.autocomp.kind != autocompMention {
		t.Fatalf("expected mention autocomplete, got kind=%d", m.autocomp.kind)
	}
	filtered := filterItems(m.autocomp.items, m.autocomp.query)
	if len(filtered) != 1 {
		t.Fatalf("expected 1 filtered file, got %d", len(filtered))
	}
	if filtered[0].Label != "test.go" {
		t.Fatalf("expected test.go, got %q", filtered[0].Label)
	}
}

func TestAutocomplete_MentionAfterSpace(t *testing.T) {
	m := newTestModel(t)
	m.home = t.TempDir()
	writeFile(t, filepath.Join(m.home, "foo.go"), "package main")

	m.input.SetValue("look at @fo")
	m.updateAutocompleteState()

	if m.autocomp.kind != autocompMention {
		t.Fatalf("expected mention autocomplete, got kind=%d", m.autocomp.kind)
	}
	filtered := filterItems(m.autocomp.items, m.autocomp.query)
	if len(filtered) != 1 {
		t.Fatalf("expected 1 filtered file, got %d", len(filtered))
	}
}

func TestAutocomplete_ArrowsWorkThroughHandleKey(t *testing.T) {
	// This tests the fix: HandleScroll must NOT eat arrow keys when autocomplete is active.
	m := newTestModel(t)
	m.commands = map[string]SlashHandler{
		"help": func(_ context.Context, _ string) (string, error) { return "", nil },
		"goal": func(_ context.Context, _ string) (string, error) { return "", nil },
		"quit": func(_ context.Context, _ string) (string, error) { return "", nil },
	}
	m.input.SetValue("/")
	m.updateAutocompleteState()

	if m.autocomp.cursor != 0 {
		t.Fatalf("expected cursor at 0, got %d", m.autocomp.cursor)
	}

	// Press down arrow via handleKey (not handleAutocompleteKey directly).
	downMsg := tea.KeyMsg{Type: tea.KeyDown}
	m2, _ := m.handleKey(downMsg)
	m3 := m2.(Model)

	if m3.autocomp.cursor != 1 {
		t.Fatalf("expected cursor to move to 1 via handleKey, got %d", m3.autocomp.cursor)
	}

	// Press up arrow.
	upMsg := tea.KeyMsg{Type: tea.KeyUp}
	m4, _ := m3.handleKey(upMsg)
	m5 := m4.(Model)

	if m5.autocomp.cursor != 0 {
		t.Fatalf("expected cursor back to 0 via handleKey, got %d", m5.autocomp.cursor)
	}
}

// --- helpers ---

func newTestModel(t *testing.T) Model {
	t.Helper()
	ti := newInputArea()
	ti.Focus()

	p := DefaultPalette()
	return Model{
		home:    t.TempDir(),
		palette: p,
		marker:  NewMarker(p),
		input:   ti,
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
