package tui

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"supercli/internal/buildinfo"
	"supercli/internal/llm"
	"supercli/internal/storage/goal"
	"supercli/internal/system/config"
	"supercli/internal/system/uilang"
	"supercli/internal/tools"
	"supercli/internal/webgui/qrcode"
)

// An explicit export request renders the actual production Model.View. Normal
// test runs write no screenshots and never launch a terminal or external UI.
func TestExportTUIScreenshots(t *testing.T) {
	directory := os.Getenv("SUPERCLI_TUI_SCREENSHOT_DIR")
	if directory == "" {
		t.Skip("screenshot export was not requested")
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		t.Fatal(err)
	}
	portableRoot, err := filepath.Abs(filepath.Join("..", "..", "..", ".tmp"))
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(portableRoot, directory)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		t.Fatal("screenshot output must remain within this repository's .tmp directory")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name string, model Model) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(directory, name+".ansi"), []byte(model.View()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	renderer := lipgloss.NewRenderer(io.Discard, termenv.WithProfile(termenv.TrueColor))
	renderer.SetColorProfile(termenv.TrueColor)
	renderer.SetHasDarkBackground(true)
	for _, language := range []string{"en", "pl"} {
		m := New(Options{Language: language, Version: "1.0.0", NoColor: false})
		m.palette = NewPalette(renderer)
		next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 34})
		m = next.(Model)
		if !strings.Contains(m.View(), "\x1b[") {
			t.Fatal("screenshot fixture did not exercise colored production rendering")
		}
		write("welcome-"+language, m)
		next, _ = m.openActionsMenu()
		m = next.(Model)
		write("actions-"+language, m)
		// Render an inert pairing fixture, never a real tunnel or launcher.
		m.enterMenu(interactiveMenu{kind: menuPhone})
		m.phone.url = phoneTestURL
		m.phone.code, err = qrcode.Encode(phoneTestURL)
		if err != nil {
			t.Fatal(err)
		}
		m.phone.status = "phone.ready"
		m.width, m.height = 110, 48
		write("phone-qr-"+language, m)
		m.width, m.height = 38, 14
		write("phone-small-"+language, m)
		// A taller fixture exposes every native name in the same real menu.
		m.width, m.height = 110, 48
		next, _ = m.openLanguageMenu()
		m = next.(Model)
		view := m.View()
		for _, supported := range uilang.Languages() {
			if !strings.Contains(view, supported.Name) {
				t.Fatalf("picker export omitted %s", supported.Code)
			}
		}
		write("languages-"+language, m)
	}

	// A conversation view in the three environments the TUI must look right
	// in: Windows Terminal (dark, true colour), a light terminal, and classic
	// cmd/conhost (256 colours, emoji replaced by plain symbols).
	variants := []struct {
		name    string
		profile termenv.Profile
		dark    bool
		legacy  bool
	}{
		{"chat-dark", termenv.TrueColor, true, false},
		{"chat-light", termenv.TrueColor, false, false},
		{"chat-cmd", termenv.ANSI256, true, true},
	}
	for _, v := range variants {
		r := lipgloss.NewRenderer(io.Discard, termenv.WithProfile(v.profile))
		r.SetColorProfile(v.profile)
		r.SetHasDarkBackground(v.dark)
		m := New(Options{Language: "pl", Version: "1.0.13", NoColor: false})
		m.palette = NewPalette(r)
		m.marker = NewMarker(m.palette, "pl")
		next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 34})
		m = next.(Model)
		m.chat.legacySymbols = v.legacy
		m.chat.addUser("> Sprawdź, czemu testy w internal/llm się sypią 📎\n\n" + attachmentDisplay([]string{"log.txt"}, "pl"))
		m.chat.addAssistant("Przejrzę **openai.go** i uruchomię testy.\n\n## Plan\n- odczyt pliku\n- `go test ./internal/llm`")
		m.appendLine(m.marker.ToolCall("read_lines", `{"file":"internal/llm/openai.go","from":310,"to":355}`))
		m.chat.addToolResultLang("read_lines", "310 | if resp.StatusCode/100 == 2 {\n311 | \tstreamCancel = cancel\n312 | \tbreak\n313 | }", "", "go")
		m.appendLine(m.marker.ToolCall("ctx_execute", `{"command":["go","test","./internal/llm"]}`))
		m.chat.addToolResultLang("ctx_execute", "", "exit status 1: TestOpenAI_Stream failed", "")
		m.chat.addAssistant("Znalazłem przyczynę: brak `continue` po ponowieniu. ✅ Poprawione.")
		m.markTranscriptPresent()
		m.refreshTranscript()
		write(v.name, m)
	}

	// Mobile/keyboard exports use only temporary, in-memory fixtures. No app
	// startup, provider calls, configuration saves or user data are involved.
	for _, size := range []struct {
		name          string
		width, height int
	}{
		{"phone", 42, 28}, {"keyboard", 38, 14}, {"tiny", 32, 12},
		{"landscape", 72, 12}, {"narrow", 60, 20}, {"desktop", 110, 34},
	} {
		for _, state := range []string{"welcome", "chat", "busy", "actions", "settings", "ask", "autocomplete"} {
			m := mobileLayoutFixture(t, size.width, size.height, state, "pl", renderer)
			write("audit-"+size.name+"-"+state, m)
		}
	}
	for _, v := range []struct {
		name    string
		profile termenv.Profile
		dark    bool
	}{
		{"light", termenv.TrueColor, false}, {"256", termenv.ANSI256, true},
		{"16", termenv.ANSI, true}, {"plain", termenv.Ascii, true},
	} {
		r := lipgloss.NewRenderer(io.Discard, termenv.WithProfile(v.profile))
		r.SetColorProfile(v.profile)
		r.SetHasDarkBackground(v.dark)
		write("audit-keyboard-chat-"+v.name, mobileLayoutFixture(t, 38, 14, "chat", "en", r))
	}
}

func mobileLayoutFixture(t *testing.T, width, height int, state, language string, renderer *lipgloss.Renderer) Model {
	t.Helper()
	m := New(Options{Home: t.TempDir(), DataDir: t.TempDir(), Language: language, Version: buildinfo.Version, NoColor: false})
	m.palette = NewPalette(renderer)
	m.marker = NewMarker(m.palette, language)
	m.llm, _ = llm.NewEcho("qwen3-coder-30b-local")
	m.dashboardFn = func() DashboardSnapshot {
		return DashboardSnapshot{
			Project: "SuperCli / terminal-demo", Directory: "~/projects/supercli-terminal-demo",
			Limits: "5h: 64% / 7d: 82%", Account: "demo@example.invalid",
			Goal:          goal.ProgressSnapshot{Title: "Czytelny terminal na telefonie i komputerze", Done: 2, Total: 4},
			SessionTokens: 12500, DailyTokens: 48700,
		}
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m = next.(Model)
	if state == "welcome" {
		return m
	}
	m.chat.addUser("Sprawdź testy i pokaż, co zostało poprawione.")
	m.chat.addAssistant("## Plan\n- sprawdzenie układu\n- testy regresji\n\n```go\nfunc greet(name string) string {\n    return \"Cześć, \" + name\n}\n```")
	m.appendLine(m.marker.ToolCall("ctx_execute", `{"command":["go","test","./internal/ui/tui"]}`))
	m.chat.addToolResultLang("ctx_execute", "ok  supercli/internal/ui/tui  0.031s", "", "")
	m.chat.addAssistant("**Gotowe.** Kolory i zawijanie tekstu zostały zachowane.\n\nMożesz otworzyć menu klawiszem Tab.")
	m.markTranscriptPresent()
	m.input.SetValue("Dopisz test wąskiego terminala.")
	m.input.CursorEnd()
	m.syncInputHeight()
	m.refreshTranscript()
	switch state {
	case "autocomplete":
		m.input.SetValue("/")
		m.updateAutocompleteState()
	case "busy":
		m.busy = true
		m.current = "Sprawdzam jeszcze długie nazwy modeli i załączniki…"
		m.resizeViewport()
	case "actions":
		next, _ = m.openActionsMenu()
		m = next.(Model)
	case "settings":
		m.enterMenu(interactiveMenu{kind: menuSettings, settingsCfg: &config.TomlConfig{}, editing: true, editBuf: "provider/long-model-name-distinctive-suffix"})
		found := false
		for category := range m.settingsCategories() {
			m.menu.category = category
			for i, row := range m.localizedSettingsRows() {
				if row.key == "task_model" {
					m.menu.cursor, found = i, true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			t.Fatal("mobile settings fixture cannot locate task_model")
		}
	case "ask":
		req := sampleRequest()
		req.Question = "Jak chcesz zastosować poprawkę?"
		for i := range req.Options {
			req.Options[i].Description = "Najpierw pokaż podgląd zmian i sprawdź testy w projekcie."
		}
		req.Options = append(req.Options, tools.AskOption{Label: "Zapisz zmiany dopiero po potwierdzeniu użytkownika", Description: "Nie wykonuj operacji automatycznie. Zachowaj aktualną konfigurację."})
		next, _ = m.beginAsk(req)
		m = next.(Model)
		m.pendingAsk.cursor = len(req.Options) - 1
	}
	return m
}
