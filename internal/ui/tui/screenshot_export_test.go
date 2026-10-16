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
	"supercli/internal/system/uilang"
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
}
