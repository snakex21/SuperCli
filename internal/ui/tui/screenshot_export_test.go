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
}
