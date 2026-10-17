//go:build !windows

package tui

import tea "github.com/charmbracelet/bubbletea"

func resizeTerminalWindow(size tea.WindowSizeMsg) tea.WindowSizeMsg { return size }
