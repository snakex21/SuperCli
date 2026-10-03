package tui

import "strings"

// Tests read canonical chat messages on demand; production retains no raw mirror.
func (m *Model) completedLines() string {
	var b strings.Builder
	for _, msg := range m.chat.msgs {
		b.WriteString(msg.text)
		b.WriteByte('\n')
	}
	return b.String()
}
