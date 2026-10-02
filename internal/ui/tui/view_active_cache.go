package tui

import (
	"reflect"
	"sync"

	"github.com/charmbracelet/lipgloss"
)

// One formatted active section is reused when only the spinner or completed
// history changes. No archived message enters this cache.
type activeSectionStyles [9]lipgloss.Style

type activeSectionKey struct {
	source                        string
	width                         int
	language                      string
	legacySymbols, thinkingFolded bool
	colors                        renderColors
}

type activeSectionCache struct {
	mu       sync.Mutex
	valid    bool
	key      activeSectionKey
	styles   activeSectionStyles
	rendered string
}

func (c *chat) clearActiveSection() {
	c.activeCache = nil
}

func (c *chat) renderActiveSection(p Palette) string {
	if c.current == "" {
		c.clearActiveSection()
		return ""
	}
	key := activeSectionKey{
		source: c.current, width: c.width, language: c.language,
		legacySymbols: c.legacySymbols, thinkingFolded: c.thinkingCollapsed,
		colors: p.renderColors(),
	}

	if c.activeCache == nil {
		c.activeCache = new(activeSectionCache)
	}
	prior := c.activeCache
	prior.mu.Lock()
	// Non-nil custom style transforms compare unequal. Recompute them so
	// stateful callbacks are never hidden behind a cached rendered string.
	if prior.valid && prior.key == key {
		styles := activeSectionPaletteStyles(p)
		if reflect.DeepEqual(&prior.styles, &styles) {
			rendered := prior.rendered
			prior.mu.Unlock()
			return rendered
		}
	}
	prior.mu.Unlock()
	rendered := renderRoleBlock(p.AssistantLabel.Render("SuperCli"),
		renderAssistantMarkdown(terminalText(c.current, c.legacySymbols), p, c.thinkingCollapsed, c.language),
		p.AssistGutter, c.width)
	prior.mu.Lock()
	prior.valid, prior.key, prior.styles, prior.rendered = true, key, activeSectionPaletteStyles(p), rendered
	prior.mu.Unlock()
	return rendered
}

func activeSectionPaletteStyles(p Palette) activeSectionStyles {
	return activeSectionStyles{
		p.Assistant, p.AssistantLabel, p.AssistGutter, p.MdH2, p.MdH3,
		p.MdCode, p.MdThinking, p.MdThinkingHeader, p.Bold,
	}
}
