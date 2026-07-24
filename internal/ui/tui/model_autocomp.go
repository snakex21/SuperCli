// Package tui is the Bubble Tea presentation layer. F25 replaces
// the raw transcript with a structured chat view (role-based
// colors), adds a status bar, inline event markers, a tool-
// name spinner, Ctrl+C run cancellation, and PgUp/PgDn scrolling.
package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// handleAutocompleteKey handles keys while the autocomplete popup is visible.
func (m Model) handleAutocompleteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	filtered := filterItems(m.autocomp.items, m.autocomp.query)

	switch msg.String() {
	case "esc":
		// Close popup, clear input back to just the trigger or empty.
		m.autocomp = autocomplete{}
		m.viewport.Height = m.viewportHeight()
		return m, nil

	case "up", "k":
		if m.autocomp.cursor > 0 {
			m.autocomp.cursor--
		}
		return m, nil

	case "down", "j":
		if m.autocomp.cursor < len(filtered)-1 {
			m.autocomp.cursor++
		}
		return m, nil

	case "enter":
		// Enter in autocomplete: fill AND execute immediately.
		if len(filtered) == 0 {
			m.autocomp = autocomplete{}
			m.viewport.Height = m.viewportHeight()
			return m, nil
		}
		it := filtered[minInt(m.autocomp.cursor, len(filtered)-1)]
		m.autocomp = autocomplete{}
		m.viewport.Height = m.viewportHeight()
		m.input.SetValue(it.Value)
		m.input.CursorEnd()
		// Dispatch the command right away.
		text := strings.TrimSpace(it.Value)
		if cmd := ParseSlashCommand(text); cmd != nil {
			return m.dispatchSlashCommand(*cmd)
		}
		return m, nil

	case "tab":
		// Tab in autocomplete: fill but let user keep typing (add args).
		if len(filtered) == 0 {
			m.autocomp = autocomplete{}
			m.viewport.Height = m.viewportHeight()
			return m, nil
		}
		it := filtered[minInt(m.autocomp.cursor, len(filtered)-1)]
		m.autocomp = autocomplete{}
		m.viewport.Height = m.viewportHeight()
		m.input.SetValue(it.Value)
		m.input.CursorEnd()
		return m, nil

	case "pgup":
		m.autocomp.cursor -= autocompMaxVisible
		if m.autocomp.cursor < 0 {
			m.autocomp.cursor = 0
		}
		return m, nil

	case "pgdown":
		m.autocomp.cursor += autocompMaxVisible
		if m.autocomp.cursor >= len(filtered) {
			m.autocomp.cursor = len(filtered) - 1
		}
		return m, nil
	}

	// For all other keys: update the textinput first, then re-evaluate trigger.
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)

	// If the trigger character is no longer in the input, close the popup.
	kind, query := splitAutocompleteTrigger(m.input.Value())
	if kind == autocompNone {
		m.autocomp = autocomplete{}
		m.viewport.Height = m.viewportHeight()
		return m, cmd
	}

	// Update filter query.
	m.autocomp.query = query
	m.autocomp.cursor = 0
	m.autocomp.scroll = 0

	// If filtered list is empty, close popup.
	if len(filterItems(m.autocomp.items, m.autocomp.query)) == 0 {
		m.autocomp = autocomplete{}
	}
	m.viewport.Height = m.viewportHeight()

	return m, cmd
}

// updateAutocompleteState checks the current input value and opens/closes
// the autocomplete popup accordingly. Called after every textinput update.
func (m *Model) updateAutocompleteState() {
	defer func() {
		m.viewport.Height = m.viewportHeight()
	}()
	text := m.input.Value()
	kind, query := splitAutocompleteTrigger(text)

	if kind == autocompNone {
		if m.autocomp.kind != autocompNone {
			m.autocomp = autocomplete{}
		}
		return
	}

	// Already showing the same kind — just update the filter.
	if m.autocomp.kind == kind {
		m.autocomp.query = query
		m.autocomp.cursor = 0
		m.autocomp.scroll = 0
		if len(filterItems(m.autocomp.items, query)) == 0 {
			m.autocomp = autocomplete{}
		}
		return
	}

	// New trigger — build items and open popup.
	switch kind {
	case autocompSlash:
		m.autocomp = autocomplete{
			kind:  autocompSlash,
			items: buildSlashItems(m.commands, m.language),
			query: query,
		}
	case autocompMention:
		m.autocomp = autocomplete{
			kind:  autocompMention,
			items: buildMentionItems(resolveAutocompleteHome(m.home), m.language),
			query: query,
		}
	}

	if len(filterItems(m.autocomp.items, query)) == 0 {
		m.autocomp = autocomplete{}
	}
}

// beginAsk switches the TUI into ask mode.
