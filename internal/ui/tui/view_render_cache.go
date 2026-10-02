package tui

import (
	"reflect"
	"sync"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Only expensive chrome is reused. View still draws the viewport, input
// contents/cursor, spinner, workers, menus and notices from their current state.
// Each entry stores all of its render inputs by value, so changing a copied
// Model or calling a mutating helper outside Update cannot retain stale chrome.
// Shared caches contain no Model, widget or slice references.
type tuiRenderCache struct {
	mu        sync.Mutex
	input     cachedInputBox
	dashboard cachedDashboard
	hints     cachedDefaultHints
}

type renderColors struct {
	renderer *lipgloss.Renderer
	profile  termenv.Profile
	dark     bool
}

func (p Palette) renderColors() renderColors {
	renderer := p.renderer
	if renderer == nil {
		renderer = lipgloss.DefaultRenderer()
	}
	return renderColors{renderer: renderer, profile: renderer.ColorProfile(), dark: renderer.HasDarkBackground()}
}

type cachedInputBox struct {
	valid             bool
	content, rendered string
	style             lipgloss.Style
	colors            renderColors
}

func (cache *tuiRenderCache) inputBox(style lipgloss.Style, colors renderColors, content string) string {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	prior := &cache.input
	if prior.valid && prior.colors == colors && prior.content == content && reflect.DeepEqual(&prior.style, &style) {
		return prior.rendered
	}
	rendered := style.Render(content)
	*prior = cachedInputBox{valid: true, content: content, rendered: rendered, style: style, colors: colors}
	return rendered
}

type dashboardRenderKey struct {
	snapshot      DashboardSnapshot
	context       contextSnapshot
	width, height int
	language      string
	colors        renderColors
}

// These are exactly the styles used by the dashboard and its cards/bars.
type dashboardRenderStyles [8]lipgloss.Style

type cachedDashboard struct {
	valid    bool
	key      dashboardRenderKey
	styles   dashboardRenderStyles
	rendered string
}

func (m Model) cachedDashboard(snapshot DashboardSnapshot) string {
	key := dashboardRenderKey{snapshot: snapshot, context: m.runtimeContext, width: m.width, height: m.height, language: m.language, colors: m.palette.renderColors()}
	styles := dashboardRenderStyles{m.palette.StatusSep, m.palette.StatusKey, m.palette.StatusDim, m.palette.HeaderMode, m.palette.StatusValue, m.palette.Success, m.palette.Error, m.palette.Panel}
	cache := m.renderCache
	cache.mu.Lock()
	defer cache.mu.Unlock()
	prior := &cache.dashboard
	// Non-nil Style transforms compare unequal, so a stateful custom transform
	// is always rendered rather than being hidden behind the cache.
	if prior.valid && prior.key == key && reflect.DeepEqual(&prior.styles, &styles) {
		return prior.rendered
	}
	rendered := m.renderDashboardSnapshot(snapshot)
	*prior = cachedDashboard{valid: true, key: key, styles: styles, rendered: rendered}
	return rendered
}

// Default hints depend only on terminal width, language and their exact style.
// Notice and scroll-back hints bypass this entry in renderHintLine; menus also
// use their own live rendering path.
type hintRenderKey struct {
	width    int
	language string
	colors   renderColors
}

type cachedDefaultHints struct {
	valid    bool
	key      hintRenderKey
	style    lipgloss.Style
	rendered string
}

func (m Model) cachedHintLine() string {
	key := hintRenderKey{width: m.width, language: m.language, colors: m.palette.renderColors()}
	style := m.palette.InputHint
	cache := m.renderCache
	cache.mu.Lock()
	defer cache.mu.Unlock()
	prior := &cache.hints
	if prior.valid && prior.key == key && reflect.DeepEqual(&prior.style, &style) {
		return prior.rendered
	}
	rendered := m.renderDefaultHintLine()
	*prior = cachedDefaultHints{valid: true, key: key, style: style, rendered: rendered}
	return rendered
}
