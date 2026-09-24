package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"time"
)

func (m *Model) setStatus(text string, success bool) {
	m.statusOverride = text
	m.statusSuccess = success
	m.statusRevision++
}
func (m Model) statusClearCmd() tea.Cmd {
	revision := m.statusRevision
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return statusOverrideClearMsg{revision: revision} })
}
func (m Model) renderNotice(width int) string {
	prefix := "! "
	style := m.palette.Error
	if m.statusSuccess {
		prefix = "+ "
		style = m.palette.Success
	}
	return style.Render(truncateVisible(prefix+m.statusOverride, width))
}
