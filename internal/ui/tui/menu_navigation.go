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
	case menuUsage:
		return m.tr("Usage", "Zużycie")
	case menuAttachments:
		return m.tr("Attachments", "Załączniki")
	case menuSessions:
		return m.tr("Sessions", "Sesje")
	case menuActions:
		return m.tr("Actions", "Działania")
	case menuModels:
		return m.tr("Choose model", "Wybierz model")
	case menuModelCatalog:
		return m.tr("Model catalog", "Katalog modeli")
	case menuProviders:
		return m.tr("Providers", "Dostawcy")
	case menuProviderPredefined:
		return m.tr("Add provider", "Dodaj dostawcę")
	case menuOpenAIAuth:
		return m.tr("Sign in", "Logowanie")
	case menuAccounts:
		return m.tr("Accounts", "Konta")
	case menuGoal:
		return m.tr("Goal", "Cel")
	case menuSettings:
		return m.tr("Settings", "Ustawienia")
	default:
		return m.tr("Back", "Wróć")
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
		return truncateVisible(m.tr("Resize terminal · Esc back", "Powiększ okno · Esc wróć"), screenWidth)
	}
	width := minInt(screenWidth, 120)
	inset := (screenWidth - width) / 2
	innerWidth := width - 4
	m.width, m.height = innerWidth, screenHeight-4
	content := strings.Split(m.renderMenuContent(), "\n")
	header := m.palette.Header.Render("SuperCli") + m.palette.Dim.Render("  /  "+m.tr("Control centre", "Centrum sterowania"))
	lines := []string{truncateVisible(header, width), m.palette.Rule.Render("╭" + strings.Repeat("─", width-2) + "╮")}
	for i := 0; i < m.height; i++ {
		line := ""
		if i < len(content) {
			line = content[i]
		}
		lines = append(lines, m.palette.Rule.Render("│ ")+fitMenuLine(line, innerWidth)+m.palette.Rule.Render(" │"))
	}
	lines = append(lines, m.palette.Rule.Render("╰"+strings.Repeat("─", width-2)+"╯"))
	back := m.tr("Esc conversation", "Esc rozmowa")
	if m.menu.parent != nil {
		back = "Esc ← " + m.menuLabel(m.menu.parent.kind)
	}
	nav := back + "   ·   " + m.tr("Ctrl+K conversation", "Ctrl+K rozmowa")
	if m.statusOverride != "" {
		nav = m.renderNotice(width)
	}
	lines = append(lines, m.palette.InputHint.Render(truncateVisible(nav, width)))
	for i := range lines {
		lines[i] = strings.Repeat(" ", inset) + lines[i]
	}
	return strings.Join(lines, "\n")
}
