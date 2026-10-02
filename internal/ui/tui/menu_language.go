package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"supercli/internal/system/config"
	"supercli/internal/system/uilang"
)

func (m Model) openLanguageMenu() (tea.Model, tea.Cmd) {
	m.enterMenu(interactiveMenu{kind: menuLanguage})
	for i, language := range uilang.Languages() {
		if language.Code == m.language {
			m.menu.cursor = i
			break
		}
	}
	return m, nil
}

func (m Model) languageRows() []uilang.Language {
	query := strings.ToLower(strings.TrimSpace(m.menu.filter))
	var rows []uilang.Language
	for _, language := range uilang.Languages() {
		if query == "" || strings.Contains(strings.ToLower(language.Name+" "+language.Code), query) {
			rows = append(rows, language)
		}
	}
	return rows
}

func (m Model) selectLanguage() (tea.Model, tea.Cmd) {
	rows := m.languageRows()
	if len(rows) == 0 {
		return m, nil
	}
	language := rows[minInt(m.menu.cursor, len(rows)-1)].Code
	if err := config.SetLanguage(m.dataDir, m.home, language); err != nil {
		m.menu.formErr = err.Error()
		return m, nil
	}
	m.language = language
	m.marker = NewMarker(m.palette, language)
	m.chat.language = language
	m.chat.completedDirty = true
	// The runtime counters are cached between turns. Rebuild their labels
	// once when the user changes language, using the same typed snapshot.
	m.refreshRuntimeHUD()
	m.input.Placeholder = m.tr("tui.menu_settings_actions.2d53d0e1de")
	if len(m.chat.msgs) > 0 {
		m.refreshTranscript()
	} else {
		m.setViewportContent(welcomeAtSize(Options{Language: language, LLM: m.llm}, m.palette, m.width, m.height))
	}
	// Returning to Settings must also show the just-saved selection.
	for previous := m.menu.parent; previous != nil; previous = previous.parent {
		if previous.settingsCfg != nil {
			previous.settingsCfg.Language = language
		}
	}
	return m.backMenu()
}

func (m Model) renderLanguageMenu() string {
	page := menuPage{title: m.tr("tui.language.title"), searchable: true,
		subtitle: m.tr("tui.language.description"), empty: m.tr("tui.language.no_match"),
		footer: m.tr("tui.language.footer")}
	for _, language := range m.languageRows() {
		badge := language.Code
		if language.Code == m.language {
			badge += " · " + m.tr("tui.language.active")
		}
		page.items = append(page.items, menuListItem{label: language.Name, badge: badge})
	}
	if m.menu.formErr != "" {
		page.detail = []string{m.menu.formErr}
	}
	return m.renderMenuPage(page)
}
