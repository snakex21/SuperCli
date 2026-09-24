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

func (m Model) renderActionCategories() string {
	return m.menuTabs(m.actionCategories(), m.menu.category)
}

func (m Model) renderActionsMenu() string {
	rows := m.filteredActionRows()
	page := menuPage{
		title:    m.tr("Action centre", "Centrum działań"),
		subtitle: m.tr("Your conversation and draft stay ready when you return.", "Rozmowa i szkic wiadomości czekają po powrocie."),
		tabs:     m.renderActionCategories(), searchable: true,
		footer: m.tr("↑↓ choose · ←→ category · Enter open", "↑↓ wybierz · ←→ kategoria · Enter otwórz"),
	}
	for _, row := range rows {
		page.items = append(page.items, menuListItem{label: row.title, badge: row.shortcut})
	}
	if len(rows) > 0 {
		row := rows[minInt(m.menu.cursor, len(rows)-1)]
		page.detailTitle = row.title
		page.detail = []string{row.desc, "", row.group}
		if row.shortcut != "" {
			page.detail = append(page.detail, "", m.tr("Shortcut: ", "Skrót: ")+row.shortcut)
		}
		page.detail = append(page.detail, "", m.tr("Enter opens this action.", "Enter otwiera wybraną opcję."))
	}
	return m.renderMenuPage(page)
}

func (m Model) renderSessionsMenu() string {
	rows := m.filteredSessionRows()
	page := menuPage{title: m.tr("Recent sessions", "Ostatnie sesje"), subtitle: m.tr("Project: ", "Projekt: ") + filepath.Base(m.home), searchable: true,
		footer: m.tr("↑↓ choose · Enter continue", "↑↓ wybierz · Enter kontynuuj"),
		empty:  m.tr("No earlier sessions in this project.", "Brak wcześniejszych sesji w tym projekcie.")}
	if m.sessionStore == nil {
		page.empty = m.tr("Session history is unavailable.", "Historia sesji jest niedostępna.")
	}
	for _, row := range rows {
		title := strings.TrimSpace(row.Title)
		if title == "" {
			title = m.tr("Untitled", "Bez tytułu")
		}
		page.items = append(page.items, menuListItem{label: title, meta: m.relativeSessionTime(row.UpdatedAt) + fmt.Sprintf(" · %d ", row.MessageCount) + m.tr("messages", "wiadomości")})
	}
	if len(rows) > 0 {
		row := rows[minInt(m.menu.cursor, len(rows)-1)]
		page.detailTitle = page.items[minInt(m.menu.cursor, len(rows)-1)].label
		page.detail = []string{m.tr("Model: ", "Model: ") + row.Model, m.tr("Provider: ", "Dostawca: ") + row.Provider, "", m.relativeSessionTime(row.UpdatedAt), "",
			m.tr("Continue from this conversation. The original session stays saved.", "Kontynuuj tę rozmowę. Oryginalna sesja pozostaje zapisana.")}
	}
	return m.renderMenuPage(page)
}

func menuWindow(total, cursor, available int) (int, int) {
	// Before Bubble Tea delivers its first WindowSizeMsg there is no known
	// height. Render the complete compact menu instead of an arbitrary five
	// rows; the next frame will apply the real terminal height.
	if available <= 0 {
		available = 16
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

func (m Model) relativeSessionTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	local := t.Local()
	now := time.Now()
	date := local.Format("2006-01-02")
	switch {
	case date == now.Format("2006-01-02"):
		return m.tr("today ", "dzisiaj ") + local.Format("15:04")
	case date == now.AddDate(0, 0, -1).Format("2006-01-02"):
		return m.tr("yesterday ", "wczoraj ") + local.Format("15:04")
	default:
		return local.Format("02.01.2006")
	}
}
