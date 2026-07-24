package tui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/llm/providers"
)

func (m Model) handleMenuKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.menu.kind == menuActions {
		return m.handleActionsKey(msg)
	}
	if m.menu.kind == menuSessions {
		return m.handleSessionsKey(msg)
	}
	if m.menu.kind == menuTranscript {
		return m.handleTranscriptKey(msg)
	}
	if m.menu.kind == menuQueue {
		return m.handleQueueKey(msg)
	}
	if m.menu.kind == menuData {
		return m.handleDataKey(msg)
	}
	if m.menu.kind == menuCheckpoint {
		switch msg.String() {
		case "esc", "n":
			return m.closeMenu()
		case "enter", "y":
			name := "undo"
			if m.menu.checkpoint != nil && m.menu.checkpoint.Redo {
				name = "redo"
			}
			return m.dispatchVisualCommand(name, "")
		}
		return m, nil
	}
	// In the provider form, most keys are text input — handle only
	// navigation/special keys here, everything else falls to rune handler.
	if m.menu.kind == menuProviderForm {
		return m.handleFormKey(msg)
	}

	// Account label prompt: typing/enter/backspace go to its own
	// handler so characters build the label instead of navigating.
	if m.menu.kind == menuAccountLabel {
		if mm, cmd, handled := m.accountLabelKey(msg.String()); handled {
			return mm, cmd
		}
	}

	// /settings integer editing: digits/enter/esc/backspace build the
	// value instead of navigating the list.
	if m.menu.kind == menuSettings && m.menu.editing {
		return m.settingsEditKey(msg)
	}

	key := msg.String()
	lowerKey := strings.ToLower(key)
	if (m.menu.kind == menuModelCatalog || m.menu.kind == menuProviderModels) && m.providerMgr != nil && (key == "A" || key == "X") {
		rows := m.filteredModelRows()
		refs := make([]providers.ModelRef, 0, len(rows))
		for _, row := range rows {
			refs = append(refs, providers.ModelRef{Provider: row.Provider, ID: row.ID})
		}
		m.providerMgr.SetModelRefsHidden(refs, key == "X")
		return m, nil
	}
	switch lowerKey {
	case "esc":
		return m.closeMenu()
	case "up", "k":
		// Only navigate if no filter is active. When filtering,
		// 'k'/'j' are regular characters.
		if m.menu.filter != "" && (lowerKey == "k" || lowerKey == "j") {
			break // fall through to rune handler
		}
		if m.menu.cursor > 0 {
			m.menu.cursor--
		}
		return m, nil
	case "down", "j":
		if m.menu.filter != "" && lowerKey == "j" {
			break
		}
		m.menu.cursor++
		m.clampMenuCursor()
		return m, nil
	case "backspace", "ctrl+h":
		if m.menu.filter != "" {
			r := []rune(m.menu.filter)
			m.menu.filter = string(r[:len(r)-1])
			m.menu.cursor = 0
		}
		return m, nil
	case "enter":
		return m.menuEnter()
	case " ":
		return m.menuSpace()
	case "right":
		// Right arrow: enable/show model.
		if isModelVisibilityMenu(m.menu.kind) && m.providerMgr != nil {
			rows := m.filteredModelRows()
			if len(rows) > 0 {
				row := rows[minInt(m.menu.cursor, len(rows)-1)]
				m.providerMgr.ShowModelFor(row.Provider, row.ID)
			}
		}
		return m, nil
	case "left":
		// Left arrow: disable/hide model.
		if isModelVisibilityMenu(m.menu.kind) && m.providerMgr != nil {
			rows := m.filteredModelRows()
			if len(rows) > 0 {
				row := rows[minInt(m.menu.cursor, len(rows)-1)]
				m.providerMgr.HideModelFor(row.Provider, row.ID)
			}
		}
		return m, nil
	case "a":
		if m.menu.kind == menuProviders {
			m.menu = interactiveMenu{kind: menuProviderPredefined}
			m.input.Blur()
			return m, nil
		}
		// Projects menu: 'a' adds the current directory (same as
		// the trailing "+ add current directory" row). Dispatched
		// through /projects add so the same path runs as the
		// slash command — one source of truth.
		if m.menu.kind == menuProjects {
			if mm, cmd, handled := m.projectsMenuKey(key); handled {
				return mm, cmd
			}
		}
		if m.menu.kind == menuGoal && m.goalSvc != nil {
			_, err := m.goalSvc.AddTask(context.Background(), "", "new task")
			if err != nil {
				m.appendLine(m.marker.Error(err))
			}
		}
		// In model menus ordinary lowercase letters belong to the filter.
		if !isModelMenu(m.menu.kind) {
			return m, nil
		}
	case "e":
		if m.menu.kind == menuProviders {
			rows := m.providerRows()
			if len(rows) > 0 {
				p := rows[minInt(m.menu.cursor, len(rows)-1)]
				apiKey := ""
				if m.providerMgr != nil {
					for _, configured := range m.providerMgr.Configured() {
						if configured.Name == p.Name {
							apiKey = configured.APIKey
							break
						}
					}
				}
				// Keep the stored key in the edit form. Rendering masks it by
				// default and the user can reveal it explicitly with Right Arrow.
				// An empty fourth field used to be submitted as an explicit clear,
				// silently deleting a working key on unrelated edits.
				m.menu = interactiveMenu{kind: menuProviderForm, editName: p.Name, form: []string{p.Name, p.Type, p.BaseURL, apiKey, p.Model}}
				m.input.Blur()
			}
		}
		if !isModelMenu(m.menu.kind) {
			return m, nil
		}
	case "d":
		// Accounts menu: 'd' logs out the selected account.
		if m.menu.kind == menuAccounts {
			if mm, cmd, handled := m.menuAccountsKey(key); handled {
				return mm, cmd
			}
		}
		// Projects menu: 'd' unregisters the selected project.
		if m.menu.kind == menuProjects {
			if mm, cmd, handled := m.projectsMenuKey(key); handled {
				return mm, cmd
			}
		}
		if m.menu.kind == menuProviders && m.providerMgr != nil {
			rows := m.providerRows()
			if len(rows) > 0 {
				_ = m.providerMgr.Remove(rows[minInt(m.menu.cursor, len(rows)-1)].Name)
				m.providerMgr.Reload()
				m.menu.cursor = 0
			}
		}
		if !isModelMenu(m.menu.kind) {
			return m, nil
		}
	case "r":
		if m.menu.kind == menuSettings {
			return m.settingsResetCurrent()
		}
		if m.menu.kind == menuModels && key == "R" {
			return m.openReasoningMenu()
		}
		if key == "R" && (m.menu.kind == menuModelCatalog || m.menu.kind == menuProviderModels) && m.providerMgr != nil && m.caps != nil {
			mgr, caps, provider := m.providerMgr, m.caps, m.menu.provider
			return m, func() tea.Msg {
				if provider == "" {
					mgr.ScanModels(caps)
				} else {
					mgr.ScanProvider(provider, caps)
				}
				return providerScanDoneMsg{}
			}
		}
		if m.menu.kind == menuProviders && m.providerMgr != nil {
			m.providerMgr.Reload()
			if m.caps == nil {
				return m, m.probeProvidersCmd()
			}
			mgr, caps := m.providerMgr, m.caps
			scan := func() tea.Msg {
				mgr.ScanModels(caps)
				return providerScanDoneMsg{}
			}
			return m, tea.Batch(m.probeProvidersCmd(), scan)
		}
		if !isModelMenu(m.menu.kind) {
			return m, nil
		}
	case "c":
		// Shortcut to the ChatGPT accounts screen — only on an
		// OpenAI/ChatGPT row (contextual, like [M]/[E]).
		if m.menu.kind == menuProviders && m.cursorOnOpenAIRow() {
			m.menu = interactiveMenu{kind: menuAccounts}
			return m, nil
		}
		if !isModelMenu(m.menu.kind) {
			return m, nil
		}
	case "m":
		if m.menu.kind == menuProviders {
			return m.openProviderModelsAtCursor()
		}
		if !isModelMenu(m.menu.kind) {
			return m, nil
		}
	}
	if len(msg.Runes) > 0 && isModelMenu(m.menu.kind) {
		// Only add printable characters to the filter.
		// Skip control characters and Alt/Meta sequences.
		for _, r := range msg.Runes {
			if r >= ' ' && r <= '~' {
				m.menu.filter += string(r)
			}
		}
		m.menu.cursor = 0
	}
	return m, nil
}

func (m Model) openProviderModelsAtCursor() (tea.Model, tea.Cmd) {
	rows := m.providerRows()
	if len(rows) == 0 {
		return m, nil
	}
	p := rows[minInt(m.menu.cursor, len(rows)-1)]
	if p.Disabled {
		m.statusOverride = "provider " + p.Name + " is paused; press Space to enable it"
		return m, statusClearCmd()
	}
	m.menu = interactiveMenu{kind: menuProviderModels, provider: p.Name}
	if mgr, caps := m.providerMgr, m.caps; mgr != nil && caps != nil {
		name := p.Name
		return m, func() tea.Msg {
			mgr.ScanProvider(name, caps)
			return providerScanDoneMsg{}
		}
	}
	return m, nil
}

// handleFormKey handles keys when editing the provider form.
// Only navigation and special keys are intercepted; every other
// key (including letters like a, d, e, r, m) goes to text input.
