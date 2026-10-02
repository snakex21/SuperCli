package tui

import tea "github.com/charmbracelet/bubbletea"

// Keyboard events avoid the receiver escape caused by focus/blur in unrelated
// event handlers. Keep value ownership and all input-mode checks here.
func (m Model) updateKeyMessage(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.attachmentPickerOpen {
		return m, nil
	}
	// Ignore Alt shortcuts so Alt+A/Alt+K do not activate menu
	// actions or insert stray ASCII. Keep non-ASCII AltGr input
	// (Polish chars like ą/ć/ł/ń/ó/ś/ż/ź) working.
	if shouldIgnoreAltKey(msg) {
		return m, nil
	}
	if msg.String() == "ctrl+c" {
		return m.handleCtrlC()
	}
	if m.resumeContext != nil {
		if msg.String() == "esc" {
			return m.handleEscCancel()
		}
		if msg.String() == "enter" {
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.syncInputHeight()
		return m, cmd
	}
	if m.mode == modeAsking {
		return m.handleAskKey(msg)
	}
	if m.mode == modeDoctor {
		return m.handleDoctorKey(msg)
	}
	if m.mode == modeMenu {
		return m.handleMenuKey(msg)
	}
	if m.busy {
		// ESC: soft-cancel current run (not the program).
		if msg.String() == "esc" {
			return m.handleEscCancel()
		}
		// T (Shift+T): toggle thinking block visibility.
		if msg.String() == "T" {
			m.chat.toggleThinking()
			m.refreshTranscript()
			return m, nil
		}
		// E (Shift+E): toggle tool result expansion.
		if msg.String() == "E" {
			m.toolExpanded = !m.toolExpanded
			m.refreshTranscript()
			return m, nil
		}
		// F25: scroll keys work even while busy.
		if HandleScroll(&m.viewport, msg, m.scroll) {
			return m, nil
		}
		return m.handleBusyInput(msg)
	}
	return m.handleKey(msg)
}
