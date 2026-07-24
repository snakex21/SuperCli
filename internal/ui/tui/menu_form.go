package tui

import (
	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/llm/providers"
)

func (m Model) handleFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m.closeMenu()
	case "up":
		m.menu.keyRevealed = false
		if m.menu.formAt > 0 {
			m.menu.formAt--
		}
		return m, nil
	case "down":
		m.menu.keyRevealed = false
		if m.menu.formAt < len(m.menu.form)-1 {
			m.menu.formAt++
		}
		return m, nil
	case "right":
		if m.menu.formAt == 3 {
			m.menu.keyRevealed = true
		}
		return m, nil
	case "left":
		if m.menu.formAt == 3 {
			m.menu.keyRevealed = false
		}
		return m, nil
	case "enter":
		return m.menuEnter()
	case "tab":
		// Tab moves to next field (same as down).
		if m.menu.formAt < len(m.menu.form)-1 {
			m.menu.formAt++
			m.menu.keyRevealed = false
		}
		return m, nil
	case "backspace", "ctrl+h":
		if len(m.menu.form) > m.menu.formAt && m.menu.form[m.menu.formAt] != "" {
			r := []rune(m.menu.form[m.menu.formAt])
			m.menu.form[m.menu.formAt] = string(r[:len(r)-1])
			m.menu.formErr = ""
		}
		return m, nil
	case "ctrl+v":
		if text, err := clipboard.ReadAll(); err == nil && text != "" {
			m.menu.form[m.menu.formAt] += normalizePastedLine(text)
			m.menu.formErr = ""
		}
		return m, nil
	}
	// Everything else — letters, digits, symbols, space — is text input.
	if len(msg.Runes) > 0 {
		text := string(msg.Runes)
		if msg.Paste {
			text = normalizePastedLine(text)
		}
		m.menu.form[m.menu.formAt] += text
		m.menu.formErr = ""
		return m, nil
	}
	return m, nil
}

func (m *Model) clampMenuCursor() {
	max := 0
	switch m.menu.kind {
	case menuActions:
		max = len(m.filteredActionRows()) - 1
	case menuSessions:
		max = len(m.filteredSessionRows()) - 1
	case menuTranscript:
		max = len(m.filteredTranscriptRows()) - 1
	case menuQueue:
		max = len(m.menu.tasks) - 1
	case menuData:
		max = 2
	case menuModels, menuModelCatalog, menuProviderModels:
		max = len(m.filteredModelRows()) - 1
	case menuProviders:
		max = len(m.providerRows()) - 1
	case menuProviderForm:
		// form uses formAt, not cursor
		max = len(m.menu.form) - 1
		if max < 0 {
			max = 0
		}
		if m.menu.formAt > max {
			m.menu.formAt = max
		}
		return
	case menuProviderPredefined:
		max = len(providers.PredefinedProviders()) - 1
	case menuOpenAIAuth:
		max = 1
	case menuAccounts:
		max = len(m.accountRows()) - 1
	case menuProjects:
		max = len(m.projectRows()) - 1
	case menuGoal:
		max = len(m.goalTaskRows()) - 1
	case menuReasoning:
		max = len(reasoningMenuOptions()) - 1
	case menuSettings:
		max = len(m.localizedSettingsRows()) - 1
	}
	if max < 0 {
		max = 0
	}
	if m.menu.cursor > max {
		m.menu.cursor = max
	}
}
