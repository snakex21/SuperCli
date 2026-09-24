package tui

import tea "github.com/charmbracelet/bubbletea"

// Mouse input is presentation-only. Wheel events over the transcript share the
// keyboard viewport and its AtBottom follow semantics; clicks never submit text.
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress ||
		(msg.Button != tea.MouseButtonWheelUp && msg.Button != tea.MouseButtonWheelDown) {
		return m, nil
	}
	if m.mode == modeMenu && (m.menu.kind == menuUsage || m.menu.kind == menuAttachments) {
		key := tea.KeyMsg{Type: tea.KeyDown}
		if msg.Button == tea.MouseButtonWheelUp {
			key.Type = tea.KeyUp
		}
		return m.handleMenuKey(key)
	}
	if m.mode != modeNormal || msg.X < 0 || msg.X >= m.width ||
		msg.Y < 1 || msg.Y >= 1+m.viewport.Height {
		return m, nil
	}
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}
