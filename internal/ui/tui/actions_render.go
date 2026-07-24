package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) dispatchVisualCommand(name, args string) (tea.Model, tea.Cmd) {
	next, _ := m.closeMenu()
	mm := next.(Model)
	return mm.dispatchSlashCommand(SlashCommand{Name: name, Args: args, Quiet: true})
}

func (m Model) renderActionsMenu() string {
	rows := m.filteredActionRows()
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render(m.tr("Action centre", "Centrum działań")) + "\n")
	b.WriteString(m.palette.Dim.Render(truncateVisible(m.tr("Common functions without typing commands. Start typing to filter.", "Najczęstsze funkcje bez wpisywania komend. Zacznij pisać, aby filtrować."), m.menuWidth())) + "\n")
	b.WriteString(m.palette.InputHint.Render(truncateVisible(m.tr("Search: ", "Szukaj: ")+m.menu.filter, m.menuWidth())) + "\n\n")
	if len(rows) == 0 {
		b.WriteString(m.palette.Dim.Render(m.tr("  No matching actions.", "  Brak pasujących działań.")) + "\n")
	} else {
		compact := m.menuWidth() < 72
		rowHeight := 1
		if compact {
			rowHeight = 2
		}
		start, end := menuWindow(len(rows), m.menu.cursor, (m.height-7)/rowHeight)
		for i := start; i < end; i++ {
			row := rows[i]
			cursor := "  "
			if i == m.menu.cursor {
				cursor = "> "
			}
			if compact {
				width := maxInt(12, m.menuWidth()-4)
				title := truncateText(row.title, width)
				first := cursor + title
				if i == m.menu.cursor {
					first = m.palette.HeaderMode.Render(first)
				} else {
					first = m.palette.Bold.Render(first)
				}
				meta := "    " + strings.ToUpper(row.group) + " · " + row.desc
				b.WriteString(truncateVisible(first, m.menuWidth()) + "\n")
				b.WriteString(m.palette.Dim.Render(truncateText(meta, width)) + "\n")
				continue
			}
			group := padRight(truncateText(strings.ToUpper(row.group), 10), 10)
			title := padRight(truncateText(row.title, 25), 25)
			descWidth := m.width - 45
			if descWidth < 16 {
				descWidth = 16
			}
			line := cursor + m.palette.StatusKey.Render(group) + " " + m.palette.Bold.Render(title) + " " + m.palette.Dim.Render(truncateText(row.desc, descWidth))
			if row.shortcut != "" && m.width >= 92 {
				line += "  " + m.palette.InputHint.Render(row.shortcut)
			}
			if i == m.menu.cursor {
				line = m.palette.HeaderMode.Render(cursor+group+" "+title) + " " + m.palette.StatusValue.Render(truncateText(row.desc, descWidth))
				if row.shortcut != "" && m.width >= 92 {
					line += "  " + m.palette.InputHint.Render(row.shortcut)
				}
			}
			b.WriteString(line + "\n")
		}
	}
	hint := m.tr("↑↓ select · Enter open · type to search · Esc back · / advanced commands", "↑↓ wybierz · Enter otwórz · pisz aby szukać · Esc wróć · / komendy zaawansowane")
	b.WriteString("\n" + m.palette.InputHint.Render(truncateVisible(hint, m.menuWidth())))
	return b.String()
}

func (m Model) renderSessionsMenu() string {
	rows := m.filteredSessionRows()
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render(m.tr("Recent sessions", "Ostatnie sesje")) + "\n")
	project := filepath.Base(m.home)
	if project == "." || project == "" {
		project = m.home
	}
	b.WriteString(m.palette.Dim.Render(truncateVisible(m.tr("Project: ", "Projekt: ")+project+m.tr(" · original sessions remain unchanged", " · oryginalne sesje pozostają bez zmian"), m.menuWidth())) + "\n")
	b.WriteString(m.palette.InputHint.Render(truncateVisible(m.tr("Search: ", "Szukaj: ")+m.menu.filter, m.menuWidth())) + "\n\n")
	if m.sessionStore == nil {
		b.WriteString(m.palette.Dim.Render(m.tr("  Session history is unavailable.", "  Historia sesji nie jest dostępna.")) + "\n")
	} else if len(rows) == 0 {
		b.WriteString(m.palette.Dim.Render(m.tr("  No earlier sessions in this project.", "  Brak wcześniejszych sesji w tym projekcie.")) + "\n")
	} else {
		compact := m.menuWidth() < 86
		rowHeight := 1
		if compact {
			rowHeight = 2
		}
		start, end := menuWindow(len(rows), m.menu.cursor, (m.height-7)/rowHeight)
		for i := start; i < end; i++ {
			row := rows[i]
			title := strings.TrimSpace(row.Title)
			if title == "" {
				title = m.tr("Untitled", "Bez tytułu")
			}
			when := relativeSessionTime(row.UpdatedAt)
			model := row.Model
			if row.Provider != "" && !strings.HasPrefix(model, row.Provider+"/") {
				model = row.Provider + "/" + model
			}
			cursor := "  "
			if i == m.menu.cursor {
				cursor = "> "
			}
			if compact {
				width := maxInt(12, m.menuWidth()-4)
				first := cursor + truncateText(title, width-2)
				meta := fmt.Sprintf("    %s · %d msg · %s", when, row.MessageCount, model)
				if i == m.menu.cursor {
					first = m.palette.HeaderMode.Render(first)
				} else {
					first = m.palette.StatusValue.Render(first)
				}
				b.WriteString(truncateVisible(first, m.menuWidth()) + "\n")
				b.WriteString(m.palette.Dim.Render(truncateText(meta, width)) + "\n")
				continue
			}
			line := cursor + padRight(truncateText(title, 38), 38) + " " + padRight(truncateText(when, 15), 15) + " " + fmt.Sprintf("%3d msg", row.MessageCount) + "  " + truncateText(model, 32)
			if i == m.menu.cursor {
				line = m.palette.HeaderMode.Render(line)
			} else {
				line = m.palette.StatusValue.Render(cursor+padRight(truncateText(title, 38), 38)) + " " + m.palette.Dim.Render(padRight(truncateText(when, 15), 15)+" "+fmt.Sprintf("%3d msg", row.MessageCount)+"  "+truncateText(model, 32))
			}
			b.WriteString(line + "\n")
		}
	}
	hint := m.tr("↑↓ select · Enter continue · type to search · Esc back", "↑↓ wybierz · Enter kontynuuj · pisz aby szukać · Esc wróć")
	b.WriteString("\n" + m.palette.InputHint.Render(truncateVisible(hint, m.menuWidth())))
	return b.String()
}

func menuWindow(total, cursor, available int) (int, int) {
	// Before Bubble Tea delivers its first WindowSizeMsg there is no known
	// height. Render the complete compact menu instead of an arbitrary five
	// rows; the next frame will apply the real terminal height.
	if available <= 0 {
		available = 16
	} else if available < 5 {
		available = 5
	}
	if available > 16 {
		available = 16
	}
	if total <= available {
		return 0, total
	}
	start := cursor - available/2
	if start < 0 {
		start = 0
	}
	if start+available > total {
		start = total - available
	}
	return start, start + available
}

func relativeSessionTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	local := t.Local()
	now := time.Now()
	y, d := local.Year(), local.YearDay()
	switch {
	case y == now.Year() && d == now.YearDay():
		return "dzisiaj " + local.Format("15:04")
	case y == now.Year() && d == now.YearDay()-1:
		return "wczoraj " + local.Format("15:04")
	default:
		return local.Format("02.01.2006")
	}
}
