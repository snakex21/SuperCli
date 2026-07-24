package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/storage/session"
)

func (m Model) filteredActionRows() []actionRow {
	q := strings.ToLower(strings.TrimSpace(m.menu.filter))
	if q == "" {
		return m.actionRows()
	}
	all := m.actionRows()
	rows := make([]actionRow, 0, len(all))
	for _, row := range all {
		haystack := strings.ToLower(row.group + " " + row.title + " " + row.desc)
		if strings.Contains(haystack, q) {
			rows = append(rows, row)
		}
	}
	return rows
}

func (m Model) filteredSessionRows() []session.Session {
	q := strings.ToLower(strings.TrimSpace(m.menu.filter))
	if q == "" {
		return m.menu.sessions
	}
	rows := make([]session.Session, 0, len(m.menu.sessions))
	for _, row := range m.menu.sessions {
		haystack := strings.ToLower(row.Title + " " + row.Model + " " + row.Provider + " " + row.ID)
		if strings.Contains(haystack, q) {
			rows = append(rows, row)
		}
	}
	return rows
}

func (m Model) filteredTranscriptRows() []transcriptMatch {
	return m.chat.search(m.menu.filter)
}

func (m Model) selectAction() (tea.Model, tea.Cmd) {
	rows := m.filteredActionRows()
	if len(rows) == 0 {
		return m, nil
	}
	switch rows[minInt(m.menu.cursor, len(rows)-1)].id {
	case "model":
		return m.openModelsMenu()
	case "models":
		return m.openModelCatalogMenu()
	case "reasoning":
		return m.openReasoningMenu()
	case "providers":
		return m.openProvidersMenu()
	case "sessions":
		return m.openSessionsMenu()
	case "transcript":
		return m.openTranscriptSearchMenu()
	case "queue":
		return m.openQueueMenu()
	case "projects":
		return m.openProjectsMenu()
	case "goal":
		return m.openGoalMenu()
	case "settings":
		return m.openSettingsMenu()
	case "data":
		return m.openDataMenu()
	case "undo":
		return m.openCheckpointMenu(false)
	case "redo":
		return m.openCheckpointMenu(true)
	case "diff", "plan", "cost", "mcp", "doctor", "workers", "help":
		return m.dispatchVisualCommand(rows[minInt(m.menu.cursor, len(rows)-1)].id, "")
	default:
		return m, nil
	}
}

func (m Model) selectSession() (tea.Model, tea.Cmd) {
	rows := m.filteredSessionRows()
	if len(rows) == 0 {
		return m, nil
	}
	return m.dispatchVisualCommand("resume", rows[minInt(m.menu.cursor, len(rows)-1)].ID)
}

func (m Model) selectTranscriptMatch() (tea.Model, tea.Cmd) {
	rows := m.filteredTranscriptRows()
	if len(rows) == 0 {
		return m, nil
	}
	selected := rows[minInt(m.menu.cursor, len(rows)-1)]
	line := m.chat.renderedLineForMessage(selected.MessageIndex, m.palette)
	m.mode = modeNormal
	m.menu = interactiveMenu{}
	m.input.Focus()
	m.refreshTranscript()
	m.viewport.SetYOffset(maxInt(0, line-1))
	return m, nil
}

func (m Model) renderTranscriptMenu() string {
	rows := m.filteredTranscriptRows()
	width := m.menuWidth()
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render(m.tr("Search conversation", "Przeszukaj rozmow\u0119")) + "\n")
	b.WriteString(m.palette.Dim.Render(truncateVisible(m.tr("Enter jumps to a block; Space folds or unfolds multi-line blocks.", "Enter przechodzi do bloku; Spacja zwija lub rozwija bloki wielowierszowe."), width)) + "\n")
	b.WriteString(m.palette.InputHint.Render(truncateVisible(m.tr("Search: ", "Szukaj: ")+m.menu.filter, width)) + "\n\n")
	if len(rows) == 0 {
		b.WriteString(m.palette.Dim.Render(m.tr("  No matching messages.", "  Brak pasuj\u0105cych wiadomo\u015bci.")) + "\n")
	} else {
		start, end := menuWindow(len(rows), m.menu.cursor, maxInt(3, m.height-7))
		for i := start; i < end; i++ {
			row := rows[i]
			who := "System"
			switch row.Role {
			case roleUser:
				who = m.tr("You", "Ty")
			case roleAssistant:
				who = "SuperCli"
			}
			fold := " "
			if m.chat.isFoldable(row.MessageIndex) {
				fold = "\u25be"
				if m.chat.msgs[row.MessageIndex].collapsed {
					fold = "\u25b8"
				}
			}
			prefix := "  "
			if i == m.menu.cursor {
				prefix = "> "
			}
			label := fmt.Sprintf("%s%s %-9s ", prefix, fold, who)
			preview := truncateText(row.Preview, maxInt(12, width-len([]rune(label))-1))
			line := m.palette.StatusKey.Render(label) + m.palette.Dim.Render(preview)
			if i == m.menu.cursor {
				line = m.palette.HeaderMode.Render(label) + m.palette.StatusValue.Render(preview)
			}
			b.WriteString(truncateVisible(line, width) + "\n")
		}
	}
	hint := m.tr("\u2191\u2193 select \u00b7 Enter jump \u00b7 Space fold \u00b7 type to search \u00b7 Esc back", "\u2191\u2193 wybierz \u00b7 Enter przejd\u017a \u00b7 Spacja zwi\u0144 \u00b7 pisz aby szuka\u0107 \u00b7 Esc wr\u00f3\u0107")
	b.WriteString("\n" + m.palette.InputHint.Render(truncateVisible(hint, width)))
	return b.String()
}
