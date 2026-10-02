package tui

import "strings"

// All post-construction content writers use this helper. Copying the model
// preserves the immutable content and independent viewport slice headers.
func (m *Model) setViewportContent(content string) {
	if m.viewportContentSet && m.viewportContent == content {
		return
	}
	m.viewport.SetContent(content)
	// Bubbles normalizes CRLF into separate storage. Do not retain the raw full
	// transcript as another cache key in that case; use its original path.
	m.viewportContentSet = !strings.Contains(content, "\r\n")
	m.viewportContent = ""
	if m.viewportContentSet {
		m.viewportContent = content
	}
}
