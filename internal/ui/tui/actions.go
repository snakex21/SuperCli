package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// actionRow is one human-facing operation in the TUI action centre. It keeps
// slash commands as an implementation detail: the user chooses an intention,
// while selectAction reuses the existing, tested command/menu path.
type actionRow struct {
	id       string
	group    string
	title    string
	desc     string
	shortcut string
}

var commonActionsEN = []actionRow{
	{id: "paste-image", group: "tui.autocomplete_render.abc7e98928", title: "tui.action_paste-image.bb68ddb623", desc: "tui.action_paste-image.293a526293"},
	{id: "attach", group: "tui.autocomplete_render.abc7e98928", title: "tui.menu_navigation.634de11477", desc: "tui.action_attach.93e8551b68", shortcut: "Ctrl+O"},
	{id: "transcript", group: "tui.action_transcript.104ab9213e", title: "tui.actions_select.42c60071a9", desc: "tui.action_transcript.fcb51b069e", shortcut: "Ctrl+F"},
	{id: "queue", group: "tui.action_transcript.104ab9213e", title: "tui.menu_workflow.6daed5b2ef", desc: "tui.action_queue.37382a2c5a"},
	{id: "model", group: "tui.action_model.5e2c614c23", title: "tui.menu_navigation.f6e05dcfcf", desc: "tui.action_model.a6f9861849"},
	{id: "models", group: "tui.action_model.5e2c614c23", title: "tui.menu_actions.1b43e2d030", desc: "tui.action_models.407e5fa48d"},
	{id: "reasoning", group: "tui.action_model.5e2c614c23", title: "tui.menu_reasoning.3236aeec43", desc: "tui.action_reasoning.67b357fe11", shortcut: "Ctrl+R"},
	{id: "providers", group: "tui.action_model.5e2c614c23", title: "tui.menu_navigation.996c32b35f", desc: "tui.action_providers.f05de39da7"},
	{id: "sessions", group: "tui.action_transcript.104ab9213e", title: "tui.actions_render.8c4f3d1307", desc: "tui.action_sessions.13c74ffd63"},
	{id: "projects", group: "tui.action_transcript.104ab9213e", title: "tui.menu_projects.04e2a9728a", desc: "tui.action_projects.0933b5bd25", shortcut: "Ctrl+P"},
	{id: "goal", group: "tui.action_transcript.104ab9213e", title: "tui.menu_goal_render.cdbf6975e8", desc: "tui.action_goal.4ffe6e0787"},
	{id: "diff", group: "tui.autocomplete_render.abc7e98928", title: "tui.action_diff.6493269cd6", desc: "tui.action_diff.47ffda45f9"},
	{id: "undo", group: "tui.autocomplete_render.abc7e98928", title: "tui.menu_checkpoint.dd8395c0e8", desc: "tui.action_undo.bceb088cc2"},
	{id: "redo", group: "tui.autocomplete_render.abc7e98928", title: "tui.menu_checkpoint.8e0f3455fe", desc: "tui.action_redo.6a8c82847b"},
	{id: "plan", group: "tui.action_plan.11b39c9377", title: "tui.action_plan.3ca7d842d1", desc: "tui.action_plan.4c667e6263"},
	{id: "cost", group: "tui.action_cost.6725e7bbcd", title: "tui.menu_navigation.8d59829c1e", desc: "tui.action_cost.39a1a28b29"},
	{id: "settings", group: "tui.action_cost.6725e7bbcd", title: "tui.menu_navigation.74a883a037", desc: "tui.action_settings.89c407b7a4"},
	{id: "data", group: "tui.action_cost.6725e7bbcd", title: "tui.menu_workflow.87e699b6c8", desc: "tui.action_data.cf37dc4a60"},
	{id: "mcp", group: "tui.action_cost.6725e7bbcd", title: "tui.action_mcp.22a7559f09", desc: "tui.action_mcp.c745084f81"},
	{id: "doctor", group: "tui.action_cost.6725e7bbcd", title: "tui.action_doctor.268f14bbfe", desc: "tui.action_doctor.67a0502d91"},
	{id: "workers", group: "tui.action_plan.11b39c9377", title: "tui.action_workers.title", desc: "tui.action_workers.7276570fb8"},
	{id: "help", group: "tui.action_cost.6725e7bbcd", title: "tui.action_help.7b145a5d2b", desc: "tui.action_help.57754ef849"},
	{id: "context-limit", group: "tui.action_model.5e2c614c23", title: "tui.action_context-limit.284d7b18b4", desc: "tui.action_context-limit.ca3ca79f01"},
	{id: "context", group: "tui.action_transcript.104ab9213e", title: "tui.action_context.81058d751b", desc: "tui.action_context.854bda7d21"},
	{id: "compact", group: "tui.action_transcript.104ab9213e", title: "tui.action_compact.afbbb87c2c", desc: "tui.action_compact.071e7593fb"},
	{id: "memory", group: "tui.action_transcript.104ab9213e", title: "tui.action_memory.dff2af70f8", desc: "tui.action_memory.f15168fc67"},
	{id: "accounts", group: "tui.action_model.5e2c614c23", title: "tui.menu_navigation.8a7c8b67fe", desc: "tui.action_accounts.90bae4f508"},
	{id: "export", group: "tui.autocomplete_render.abc7e98928", title: "tui.action_export.5d974f9e80", desc: "tui.action_export.9c89437499"},
	{id: "update", group: "tui.action_update.group", title: "update.check", desc: "tui.action_update.desc"},
}

// The legacy name is retained for package tests; rows store catalog keys.
var commonActions = commonActionsEN

func (m Model) actionRows() []actionRow {
	rows := append([]actionRow(nil), commonActionsEN...)
	for i := range rows {
		rows[i].group = m.tr(rows[i].group)
		rows[i].title = m.tr(rows[i].title)
		rows[i].desc = m.tr(rows[i].desc)
	}
	return rows
}

// ActionIDs returns the discoverable intent-first actions. It is used by the
// cross-surface contract test; the TUI itself still renders the richer rows.
func ActionIDs() []string {
	out := make([]string, 0, len(commonActionsEN))
	for _, row := range commonActionsEN {
		out = append(out, row.id)
	}
	return out
}

func (m Model) openActionsMenu() (tea.Model, tea.Cmd) {
	m.enterMenu(interactiveMenu{kind: menuActions})
	return m, nil
}

func (m Model) openSessionsMenu() (tea.Model, tea.Cmd) {
	m.enterMenu(interactiveMenu{kind: menuSessions})
	if m.sessionStore == nil {
		return m, nil
	}
	rows, err := m.sessionStore.ListByCwd(m.home, 60)
	if err != nil {
		m.setStatus(m.tr("tui.actions.25792445a5")+err.Error(), false)
		return m, nil
	}
	filtered := rows[:0]
	for _, row := range rows {
		if row.ID != m.sessionID {
			filtered = append(filtered, row)
		}
	}
	m.menu.sessions = filtered
	return m, nil
}

func (m Model) actionCategories() []string {
	return []string{m.tr("tui.categories.a52ace420f"), m.tr("tui.action_transcript.104ab9213e"), m.tr("tui.action_model.5e2c614c23"), m.tr("tui.autocomplete_render.abc7e98928"), m.tr("tui.action_plan.11b39c9377"), m.tr("tui.action_cost.6725e7bbcd")}
}

func (m Model) handleActionsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "left", "shift+tab", "right", "tab":
		delta := 1
		if msg.String() == "left" || msg.String() == "shift+tab" {
			delta = -1
		}
		n := len(m.actionCategories())
		m.menu.category = (m.menu.category + delta + n) % n
		m.menu.cursor = 0
		m.menu.filter = ""
		return m, nil
	}
	return m.handleSearchMenuKey(msg, func() int { return len(m.filteredActionRows()) }, m.selectAction)
}

func (m Model) handleSessionsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	return m.handleSearchMenuKey(msg, func() int { return len(m.filteredSessionRows()) }, m.selectSession)
}

func (m Model) openTranscriptSearchMenu() (tea.Model, tea.Cmd) {
	m.enterMenu(interactiveMenu{kind: menuTranscript})
	return m, nil
}

func (m Model) handleTranscriptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == " " {
		rows := m.filteredTranscriptRows()
		if len(rows) > 0 {
			m.chat.toggleMessage(rows[minInt(m.menu.cursor, len(rows)-1)].MessageIndex)
		}
		return m, nil
	}
	return m.handleSearchMenuKey(msg, func() int { return len(m.filteredTranscriptRows()) }, m.selectTranscriptMatch)
}

// handleSearchMenuKey is shared by the two intent-first menus. Deliberately
// only arrow keys navigate: ordinary letters always filter, so users never
// need to learn vi keys or command names.
func (m Model) handleSearchMenuKey(msg tea.KeyMsg, count func() int, selectRow func() (tea.Model, tea.Cmd)) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m.backMenu()
	case "ctrl+k":
		return m.closeMenu()
	case "up":
		if m.menu.cursor > 0 {
			m.menu.cursor--
		}
		return m, nil
	case "down":
		if m.menu.cursor+1 < count() {
			m.menu.cursor++
		}
		return m, nil
	case "home":
		m.menu.cursor = 0
		return m, nil
	case "end":
		m.menu.cursor = maxInt(0, count()-1)
		return m, nil
	case "pgup":
		m.menu.cursor -= 8
		if m.menu.cursor < 0 {
			m.menu.cursor = 0
		}
		return m, nil
	case "pgdown":
		m.menu.cursor += 8
		if n := count(); n == 0 {
			m.menu.cursor = 0
		} else if m.menu.cursor >= n {
			m.menu.cursor = n - 1
		}
		return m, nil
	case "backspace", "ctrl+h":
		r := []rune(m.menu.filter)
		if len(r) > 0 {
			m.menu.filter = string(r[:len(r)-1])
			m.menu.cursor = 0
		}
		return m, nil
	case "enter":
		return selectRow()
	}
	if len(msg.Runes) > 0 {
		for _, r := range msg.Runes {
			if r >= ' ' && r != 0x7f {
				m.menu.filter += string(r)
			}
		}
		m.menu.cursor = 0
	}
	return m, nil
}
