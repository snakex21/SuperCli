package tui

import (
	"reflect"
	"slices"
)

// Snapshots are immutable because copied models can share the messages slice.
// Only the unmodified built-in palette permits extending a rendered prefix;
// custom styles keep their full render and callback ordering.
type completedHistorySnapshot struct {
	messages                                        []msg
	width                                           int
	language                                        string
	legacySymbols, thinkingCollapsed, toolsExpanded bool
	palette                                         Palette
	colors                                          renderColors
	rendered                                        string
}

func (c *chat) completedPrefix(p Palette) (int, string, bool) {
	prior := c.completedSnapshot
	if prior == nil || !reflect.DeepEqual(&prior.palette, &p) {
		return 0, "", false
	}
	if prior.width != c.width || prior.language != c.language || prior.legacySymbols != c.legacySymbols || prior.thinkingCollapsed != c.thinkingCollapsed || prior.toolsExpanded != c.toolsExpanded || prior.colors != p.renderColors() || len(c.msgs) < len(prior.messages) || !slices.Equal(c.msgs[:len(prior.messages)], prior.messages) {
		return 0, "", true
	}
	return len(prior.messages), prior.rendered, true
}

func (c *chat) rememberCompleted(p Palette, stock bool) {
	if len(c.msgs) == 0 {
		c.completedSnapshot = nil
		return
	}
	if !stock && p.renderer != nil {
		builtin := NewPalette(p.renderer)
		stock = reflect.DeepEqual(&builtin, &p)
	}
	if !stock {
		c.completedSnapshot = nil
		return
	}
	c.completedSnapshot = &completedHistorySnapshot{
		messages: slices.Clone(c.msgs), width: c.width, language: c.language,
		legacySymbols: c.legacySymbols, thinkingCollapsed: c.thinkingCollapsed,
		toolsExpanded: c.toolsExpanded, palette: p, colors: p.renderColors(), rendered: c.completedCache,
	}
}
