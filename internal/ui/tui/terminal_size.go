package tui

import tea "github.com/charmbracelet/bubbletea"

// Windows buffer-resize events describe scrollback storage, not the visible
// terminal. Invalid/unavailable measurements must preserve the original event.
func resizeToViewport(original tea.WindowSizeMsg, width, height int) tea.WindowSizeMsg {
	if width <= 0 || height <= 0 {
		return original
	}
	return tea.WindowSizeMsg{Width: width, Height: height}
}
