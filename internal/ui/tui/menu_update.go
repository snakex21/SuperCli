package tui

import tea "github.com/charmbracelet/bubbletea"

func (m Model) openUpdateMenu() (tea.Model, tea.Cmd) {
	m.enterMenu(interactiveMenu{kind: menuUpdate})
	return m, nil
}

func (m Model) selectUpdateAction() (tea.Model, tea.Cmd) {
	actions := []string{"check", "download", "install"}
	return m.dispatchVisualCommand("update", actions[minInt(m.menu.cursor, len(actions)-1)])
}

func (m Model) renderUpdateMenu() string {
	page := menuPage{title: m.tr("update.check"), footer: m.tr("tui.menu_reasoning.5c07cbff09")}
	for _, key := range []string{"update.check", "update.download", "update.install"} {
		page.items = append(page.items, menuListItem{label: m.tr(key)})
	}
	return m.renderMenuPage(page)
}
