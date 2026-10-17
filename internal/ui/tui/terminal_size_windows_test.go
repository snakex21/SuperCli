//go:build windows

package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/sys/windows"
)

func TestTerminalResizeUsesVisibleConsoleRectangle(t *testing.T) {
	var info windows.ConsoleScreenBufferInfo
	info.Size.X, info.Size.Y = 160, 9001
	info.Window.Left, info.Window.Right = 10, 129
	info.Window.Top, info.Window.Bottom = 8900, 8929
	original := tea.WindowSizeMsg{Width: 160, Height: 9001}
	got := resizeFromConsoleViewport(original, info)
	if got.Width != 120 || got.Height != 30 {
		t.Fatalf("used buffer storage instead of the scrolled viewport: %+v", got)
	}
	info.Window.Right = info.Window.Left - 1
	if got := resizeFromConsoleViewport(original, info); got != original {
		t.Fatalf("invalid console rectangle replaced a valid event: %+v", got)
	}
}
