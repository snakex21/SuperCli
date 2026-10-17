//go:build windows

package tui

import (
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/sys/windows"
)

// Bubble Tea forwards WINDOW_BUFFER_SIZE_EVENT.Size directly. Classic conhost
// commonly has 9001 buffer rows but only 30 visible rows. Read the output
// viewport, just as Bubble Tea's initial terminal-size query does.
func resizeTerminalWindow(original tea.WindowSizeMsg) tea.WindowSizeMsg {
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(windows.Handle(os.Stdout.Fd()), &info); err != nil {
		return original // Redirected output and non-console writers.
	}
	return resizeFromConsoleViewport(original, info)
}

func resizeFromConsoleViewport(original tea.WindowSizeMsg, info windows.ConsoleScreenBufferInfo) tea.WindowSizeMsg {
	width := int(info.Window.Right) - int(info.Window.Left) + 1
	height := int(info.Window.Bottom) - int(info.Window.Top) + 1
	return resizeToViewport(original, width, height)
}
