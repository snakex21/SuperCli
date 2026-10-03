package tui

import (
	"strings"
	"time"
)

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
	m.nativeReasoningEnd = 0
	m.current = ""
	m.currentBuffer = nil
	m.chat.current = ""
	m.chat.clearActiveSection()
}

func (m *Model) streamSpinner() string {
	if m.busy && m.current != "" {
		return m.spinner.View()
	}
	return ""
}

func (m *Model) closeReasoning() {
	if m.reasoningOpen {
		m.nativeReasoningEnd = len(m.current)
		m.appendStreamText("</thinking>\n")
		m.reasoningOpen = false
	}
}

// Native reasoning may be reported after prose or delivered in delayed fragments.
// Keep its host-created leading block before the unchanged visible answer.
func (m *Model) appendReasoningText(text string) {
	if m.reasoningOpen {
		m.appendStreamText(text)
		return
	}
	if m.current == "" {
		m.appendStreamText("<thinking>" + text)
		m.reasoningOpen = true
		return
	}
	end := m.nativeReasoningEnd
	if end > 0 && end+len("</thinking>") <= len(m.current) && strings.HasPrefix(m.current, "<thinking>") && m.current[end:end+len("</thinking>")] == "</thinking>" {
		m.current = m.current[:end] + text + m.current[end:]
		m.nativeReasoningEnd += len(text)
	} else {
		m.current = "<thinking>" + text + "</thinking>" + m.current
		m.nativeReasoningEnd = len("<thinking>") + len(text)
	}
	// A splice is not a trusted append to the old stream buffer. Copied models
	// retain their immutable current string and independently owned next buffer.
	m.currentBuffer = nil
	m.chat.current = m.current
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

// Reuse the existing frame tick to finish dense bursts. Preparing at most twice
// per terminal frame keeps latency low without formatting every provider delta.
// Short answers, sparse fragments and the first text of each section are immediate.
const streamFrameInterval = 16 * time.Millisecond

func (m *Model) refreshStreamTranscript(first bool) {
	if first || len(m.current) < 1024 || !m.busy || m.eventCh == nil || m.streamPaintAt.IsZero() || time.Since(m.streamPaintAt) >= streamFrameInterval/2 {
		m.refreshTranscript()
	}
}
