package tui

import (
	"github.com/charmbracelet/bubbles/viewport"
)

// ScrollConfig holds the keyboard scroll behavior. It is
// attached to the Model and configured once at startup.
type ScrollConfig struct {
	// PageLines is how many lines PgUp/PgDn scroll. Defaults to
	// viewport half-height when 0.
	PageLines int
}

// teaKeyMsg is a minimal interface matching tea.KeyMsg's
// String() method. This avoids importing tea in this file
// and keeps the function testable.
type teaKeyMsg interface {
	String() string
}

// HandleScroll interprets a key message for viewport scrolling.
// Returns true if the key was consumed (scrolled).
func HandleScroll(vp *viewport.Model, msg teaKeyMsg, cfg ScrollConfig) bool {
	switch msg.String() {
	case "pgup":
		vp.HalfViewUp()
		return true
	case "pgdown":
		vp.HalfViewDown()
		return true
	case "up":
		vp.LineUp(1)
		return true
	case "down":
		vp.LineDown(1)
		return true
	case "home":
		vp.GotoTop()
		return true
	case "end":
		vp.GotoBottom()
		return true
	}
	return false
}
