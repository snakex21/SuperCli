package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"strings"
)

// enterMenu retains only UI state. Going back never repeats provider probes or
// discards the draft in the chat input.
func (m *Model) enterMenu(next interactiveMenu) {
	if m.mode == modeMenu && m.menu.kind != menuNone {
		if m.menu.kind == next.kind {
			next.parent = m.menu.parent
		} else {
			previous := m.menu
			next.parent = &previous
		}
	}
	m.mode = modeMenu
	m.menu = next
	m.autocomp = autocomplete{}
	m.input.Blur()
}

func (m Model) backMenu() (tea.Model, tea.Cmd) {
	m.cancelProviderDetection()
	if m.menu.parent == nil {
		return m.closeMenu()
	}
	m.menu = *m.menu.parent
	m.clampMenuCursor()
	return m, nil
}

// After saving a provider, discard its completed form and return to the
// original provider list, without leaving stale forms on the back path.
func (m *Model) returnToProvidersMenu() {
	for previous := m.menu.parent; previous != nil; previous = previous.parent {
		if previous.kind == menuProviders {
			m.menu = *previous
			m.clampMenuCursor()
			return
		}
	}
	m.menu = interactiveMenu{kind: menuProviders}
}

func (m Model) menuLabel(kind menuKind) string {
	switch kind {
	case menuPreview:
		return m.tr("preview.title")
	case menuUsage:
		return m.tr("tui.menu_navigation.8d59829c1e")
	case menuAttachments:
		return m.tr("tui.menu_navigation.634de11477")
	case menuSessions:
		return m.tr("tui.menu_navigation.6fa3cbf451")
	case menuActions:
		return m.tr("tui.menu_navigation.ff8059dc67")
	case menuModels:
		return m.tr("tui.menu_navigation.f6e05dcfcf")
	case menuModelCatalog:
		return m.tr("tui.menu_actions.1b43e2d030")
	case menuProviders:
		return m.tr("tui.menu_navigation.996c32b35f")
	case menuProviderPredefined:
		return m.tr("tui.menu_navigation.8cd1856b03")
	case menuOpenAIAuth:
		return m.tr("tui.menu_navigation.bfd402b2f6")
	case menuAccounts:
		return m.tr("tui.menu_navigation.8a7c8b67fe")
	case menuGoal:
		return m.tr("tui.menu_goal_render.cdbf6975e8")
	case menuSettings:
		return m.tr("tui.menu_navigation.74a883a037")
	case menuLanguage:
		return m.tr("tui.language.title")
	case menuUpdate:
		return m.tr("update.check")
	default:
		return m.tr("tui.menu_navigation.76900f1bfd")
	}
}

// Each page occupies a stable rectangle; switching menus cannot leave stale
// rows behind or push the input into terminal scrollback.
func (m Model) renderMenuView() string {
	screenWidth, screenHeight := m.width, m.height
	if screenWidth <= 0 {
		screenWidth = 100
	}
	if screenHeight <= 0 {
		screenHeight = 32
	}
	if screenWidth < 16 || screenHeight < 8 {
		return truncateVisible(m.tr("tui.menu_navigation.23ef7dadd9"), screenWidth)
	}
	width := minInt(screenWidth, 120)
	inset := (screenWidth - width) / 2
	innerWidth := width - 4
	m.width, m.height = innerWidth, screenHeight-4
	content := strings.Split(m.renderMenuContent(), "\n")
	header := m.palette.Header.Render("SuperCli") + m.palette.Dim.Render("  /  "+m.tr("tui.menu_navigation.c75923d3b8"))
	lines := []string{truncateVisible(header, width), m.palette.Rule.Render("╭" + strings.Repeat("─", width-2) + "╮")}
	for i := 0; i < m.height; i++ {
		line := ""
		if i < len(content) {
			line = content[i]
		}
		lines = append(lines, m.palette.Rule.Render("│ ")+fitMenuLine(line, innerWidth)+m.palette.Rule.Render(" │"))
	}
	lines = append(lines, m.palette.Rule.Render("╰"+strings.Repeat("─", width-2)+"╯"))
	back := m.tr("tui.menu_navigation.25ba330f4e")
	if m.menu.parent != nil {
		back = "Esc ← " + m.menuLabel(m.menu.parent.kind)
	}
	nav := back + "   ·   " + m.tr("tui.menu_navigation.e4b2784215")
	if m.statusOverride != "" {
		nav = m.renderNotice(width)
	}
	lines = append(lines, m.palette.InputHint.Render(truncateVisible(nav, width)))
	for i := range lines {
		lines[i] = strings.Repeat(" ", inset) + lines[i]
	}
	return strings.Join(lines, "\n")
}
