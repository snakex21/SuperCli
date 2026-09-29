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
		title:    m.tr("tui.actions_render.2ac463151e"),
		subtitle: m.tr("tui.actions_render.80e7b49c59"),
		tabs:     m.renderActionCategories(), searchable: true,
		footer: m.tr("tui.actions_render.a96cd43c0d"),
	}
	for _, row := range rows {
		page.items = append(page.items, menuListItem{label: row.title, badge: row.shortcut})
	}
	if len(rows) > 0 {
		row := rows[minInt(m.menu.cursor, len(rows)-1)]
		page.detailTitle = row.title
		page.detail = []string{row.desc, "", row.group}
		if row.shortcut != "" {
			page.detail = append(page.detail, "", m.tr("tui.actions_render.aedc456519")+row.shortcut)
		}
		page.detail = append(page.detail, "", m.tr("tui.actions_render.940d593d2b"))
	}
	return m.renderMenuPage(page)
}

func (m Model) renderSessionsMenu() string {
	rows := m.filteredSessionRows()
	page := menuPage{title: m.tr("tui.actions_render.8c4f3d1307"), subtitle: m.tr("tui.actions_render.670c99a6bb") + filepath.Base(m.home), searchable: true,
		footer: m.tr("tui.actions_render.fbbd0db52f"),
		empty:  m.tr("tui.actions_render.c4c363be98")}
	if m.sessionStore == nil {
		page.empty = m.tr("tui.actions_render.b4d7083592")
	}
	for _, row := range rows {
		title := strings.TrimSpace(row.Title)
		if title == "" {
			title = m.tr("tui.actions_render.f59ab8d133")
		}
		page.items = append(page.items, menuListItem{label: title, meta: m.relativeSessionTime(row.UpdatedAt) + fmt.Sprintf(" · %d ", row.MessageCount) + m.tr("tui.actions_render.f5cccfb737")})
	}
	if len(rows) > 0 {
		row := rows[minInt(m.menu.cursor, len(rows)-1)]
		page.detailTitle = page.items[minInt(m.menu.cursor, len(rows)-1)].label
		page.detail = []string{m.tr("tui.actions_render.33f4e5313c") + row.Model, m.tr("tui.actions_render.e0f3fde9df") + row.Provider, "", m.relativeSessionTime(row.UpdatedAt), "",
			m.tr("tui.actions_render.2d1cc98c10")}
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
		return m.tr("tui.actions_render.03b4c6f8e4") + local.Format("15:04")
	case date == now.AddDate(0, 0, -1).Format("2006-01-02"):
		return m.tr("tui.actions_render.5b7b617857") + local.Format("15:04")
	default:
		return local.Format("02.01.2006")
	}
}
