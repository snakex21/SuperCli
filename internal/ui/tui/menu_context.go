package tui

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"supercli/internal/system/config"
)

var contextMenuValues = []string{"auto", "32k", "64k", "100k", "128k", "256k", "1m", "custom"}

func (m Model) openContextLimitMenu() (tea.Model, tea.Cmd) {
	m.enterMenu(interactiveMenu{kind: menuContextLimit})
	return m, nil
}
func (m Model) handleContextLimitKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.menu.editing {
		switch msg.String() {
		case "esc":
			m.menu.editing = false
			m.menu.formErr = ""
			return m, nil
		case "backspace", "ctrl+h":
			r := []rune(m.menu.editBuf)
			if len(r) > 0 {
				m.menu.editBuf = string(r[:len(r)-1])
			}
			return m, nil
		case "enter":
			if _, _, err := config.ParseContextBudget(m.menu.editBuf); err != nil {
				m.menu.formErr = m.tr("tui.menu_context.eba106b358")
				return m, nil
			}
			return m.dispatchVisualCommand("context-limit", m.menu.editBuf)
		}
		if len(msg.Runes) > 0 {
			m.menu.editBuf += string(msg.Runes)
		}
		return m, nil
	}
	switch msg.String() {
	case "esc":
		return m.backMenu()
	case "up":
		m.menu.cursor = maxInt(0, m.menu.cursor-1)
	case "down":
		m.menu.cursor = minInt(len(contextMenuValues)-1, m.menu.cursor+1)
	case "pgup":
		m.menu.cursor = maxInt(0, m.menu.cursor-5)
	case "pgdown":
		m.menu.cursor = minInt(len(contextMenuValues)-1, m.menu.cursor+5)
	case "home":
		m.menu.cursor = 0
	case "end":
		m.menu.cursor = len(contextMenuValues) - 1
	case "enter":
		value := contextMenuValues[m.menu.cursor]
		if value == "custom" {
			m.menu.editing = true
			m.menu.editBuf = ""
			return m, nil
		}
		return m.dispatchVisualCommand("context-limit", value)
	}
	return m, nil
}
func (m Model) renderContextLimitMenu() string {
	current := "auto"
	if m.modelContexts != nil {
		if tokens, ok := m.modelContexts.Get(m.activeProviderName(), m.reasoningModelName()); ok {
			current = fmt.Sprint(tokens)
		}
	}
	page := menuPage{title: m.tr("tui.menu_context.2b7f5a0974"), subtitle: m.reasoningModelName() + " · " + current,
		detailTitle: m.tr("tui.menu_context.54a9d031cd"),
		detail: []string{m.tr("tui.menu_context.a936ab3e43"), "",
			m.activeProviderName(), m.reasoningModelName(), "", m.tr("tui.menu_context.f156069983") + current},
		footer: m.tr("tui.menu_context.87709f561c")}
	for _, value := range contextMenuValues {
		label := value
		if value == "custom" {
			label = m.tr("tui.menu_context.b72813b729")
			if m.menu.editing {
				label = m.menu.editBuf + "▏"
			}
		}
		page.items = append(page.items, menuListItem{label: label})
	}
	if m.menu.editing {
		page.footer = m.tr("tui.menu_context.cf88b00a0e")
	}
	if m.menu.formErr != "" {
		page.detail = append([]string{m.menu.formErr, ""}, page.detail...)
	}
	return m.renderMenuPage(page)
}
