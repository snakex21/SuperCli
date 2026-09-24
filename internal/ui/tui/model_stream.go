package tui

import "strings"

// appendStreamText owns one growable buffer and exposes immutable string
// snapshots to the chat. Appending never overwrites a previously emitted prefix.
// If a copied Model is advanced independently, detach from its newer buffer.
func (m *Model) appendStreamText(text string) {
	if text == "" {
		return
	}
	if m.currentBuffer == nil || m.currentBuffer.String() != m.current {
		m.currentBuffer = new(strings.Builder)
		m.currentBuffer.WriteString(m.current)
	}
	m.currentBuffer.WriteString(text)
	m.current = m.currentBuffer.String()
	m.chat.current = m.current
}

func (m *Model) resetCurrent() {
	m.reasoningOpen = false
	m.current = ""
	m.currentBuffer = nil
	m.chat.current = ""
}

func (m *Model) streamSpinner() string {
	if m.busy && m.current != "" {
		return m.spinner.View()
	}
	return ""
}

func (m *Model) closeReasoning() {
	if m.reasoningOpen {
		m.appendStreamText("</thinking>\n")
		m.reasoningOpen = false
	}
}

// Resizing chrome must preserve follow mode; AtBottom after shrinking would
// incorrectly treat the previous bottom as a deliberate scroll into history.
func (m *Model) resizeViewport() {
	follow := m.viewport.AtBottom()
	m.viewport.Height = m.viewportHeight()
	if follow {
		m.viewport.GotoBottom()
	}
}
