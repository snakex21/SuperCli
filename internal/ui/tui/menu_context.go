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
				m.menu.formErr = m.tr("Use a value such as 100k, 1m or auto.", "Podaj np. 100k, 1m lub auto.")
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
	page := menuPage{title: m.tr("Model context", "Kontekst modelu"), subtitle: m.reasoningModelName() + " · " + current,
		detailTitle: m.tr("Context budget", "Budżet kontekstu"),
		detail: []string{m.tr("Set the context limit for this provider and model.", "Ustaw limit kontekstu dla tego dostawcy i modelu."), "",
			m.activeProviderName(), m.reasoningModelName(), "", m.tr("Current: ", "Aktualnie: ") + current},
		footer: m.tr("↑↓ choose · Enter save", "↑↓ wybierz · Enter zapisz")}
	for _, value := range contextMenuValues {
		label := value
		if value == "custom" {
			label = m.tr("Custom value…", "Własna wartość…")
			if m.menu.editing {
				label = m.menu.editBuf + "▏"
			}
		}
		page.items = append(page.items, menuListItem{label: label})
	}
	if m.menu.editing {
		page.footer = m.tr("Enter save · Esc cancel", "Enter zapisz · Esc anuluj")
	}
	if m.menu.formErr != "" {
		page.detail = append([]string{m.menu.formErr, ""}, page.detail...)
	}
	return m.renderMenuPage(page)
}
