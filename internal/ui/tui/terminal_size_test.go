package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestTerminalResizePreservesUnknownViewport(t *testing.T) {
	original := tea.WindowSizeMsg{Width: 120, Height: 9001}
	for _, size := range [][2]int{{0, 30}, {120, 0}, {-1, 30}, {120, -1}} {
		if got := resizeToViewport(original, size[0], size[1]); got != original {
			t.Fatalf("invalid viewport %v changed the event: %+v", size, got)
		}
	}
}

func consoleResizeFixture(t *testing.T) (before, after string) {
	t.Helper()
	p := NoColorPalette()
	m := onboardModel{step: onboardMenu, language: "ru", choices: buildChoices(nil, "ru"), palette: &p}
	buffer := tea.WindowSizeMsg{Width: 160, Height: 9001}
	next, _ := m.Update(buffer)
	m = next.(onboardModel)
	lines := strings.Split(m.View(), "\n")
	before = strings.Join(lines[len(lines)-30:], "\n")
	// This is the reported shape: all content is above the visible window;
	// only the padded split separator and the final keyboard hints remain.
	if strings.Contains(ansi.Strip(before), "SuperCli") || !strings.Contains(before, "│") || !strings.Contains(before, "Tab") {
		t.Fatal("buffer-height fixture did not reproduce the footer-only screen")
	}
	f := terminalKeyFilter{resize: func(event tea.WindowSizeMsg) tea.WindowSizeMsg {
		return resizeToViewport(event, 120, 30)
	}}
	message := f.filter(nil, buffer)
	if got, ok := message.(tea.WindowSizeMsg); !ok || got.Width != 120 || got.Height != 30 {
		t.Fatalf("resize event was not normalized before the model: %+v", message)
	}
	next, _ = m.Update(message)
	m = next.(onboardModel)
	after = m.View()
	if lipgloss.Height(after) > 30 || lipgloss.Width(after) > 120 {
		t.Fatalf("viewport render is oversized: %dx%d", lipgloss.Width(after), lipgloss.Height(after))
	}
	if !strings.Contains(after, "SuperCli") || !strings.Contains(after, m.choices[0].label) || !strings.Contains(after, "Tab") {
		t.Fatal("normalized viewport lost the header, providers or keyboard hints")
	}
	return before, after
}

func TestTerminalResizeUsesViewportInsteadOfScrollback(t *testing.T) {
	consoleResizeFixture(t)
}

// Opt-in visual evidence, using only the local provider catalog and plain text.
func TestExportConsoleResizeScreenshots(t *testing.T) {
	dir := os.Getenv("SUPERCLI_TUI_SCREENSHOT_DIR")
	if dir == "" {
		t.Skip("set SUPERCLI_TUI_SCREENSHOT_DIR to export console resize fixtures")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	before, after := consoleResizeFixture(t)
	for name, view := range map[string]string{"console-buffer-before": before, "console-viewport-after": after} {
		if err := os.WriteFile(filepath.Join(dir, name+".ansi"), []byte(view), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
