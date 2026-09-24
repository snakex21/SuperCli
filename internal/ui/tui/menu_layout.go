package tui

import (
	"fmt"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"strings"
)

// Rendering is presentation only: no provider probes or model calls.
type menuListItem struct{ label, meta, badge string }
type menuPage struct {
	title, subtitle, tabs string
	searchable            bool
	items                 []menuListItem
	detailTitle           string
	detail                []string
	footer, empty         string
}

func fitMenuLine(s string, width int) string {
	if width <= 0 {
		return ""
	}
	s = truncateVisible(strings.ReplaceAll(s, "\n", " "), width)
	return s + strings.Repeat(" ", maxInt(0, width-lipgloss.Width(s)))
}

func (m Model) menuTabs(labels []string, selected int) string {
	selected = minInt(maxInt(0, selected), len(labels)-1)
	parts := make([]string, len(labels))
	for i, label := range labels {
		if i == selected {
			parts[i] = m.palette.MenuTab.Render(" " + label + " ")
		} else {
			parts[i] = m.palette.Dim.Render(" " + label + " ")
		}
	}
	line := strings.Join(parts, " ")
	if lipgloss.Width(line) > m.menuWidth() {
		line = "← " + parts[selected] + " →"
	}
	return truncateVisible(line, m.menuWidth())
}

func (m Model) renderMenuPage(page menuPage) string {
	width, height := m.menuWidth(), m.height
	if height <= 0 {
		height = 28
	}
	height = maxInt(4, height)
	position := "0/0"
	cursor := minInt(maxInt(0, m.menu.cursor), maxInt(0, len(page.items)-1))
	if len(page.items) > 0 {
		position = fmt.Sprintf("%d/%d", cursor+1, len(page.items))
	}
	head := m.palette.PanelTitle.Render(truncateVisible(page.title, maxInt(1, width-len(position)-2)))
	head = fitMenuLine(head, maxInt(1, width-len(position))) + m.palette.Dim.Render(position)
	lines := []string{head}
	if page.subtitle != "" && height >= 12 {
		lines = append(lines, m.palette.Dim.Render(truncateVisible(page.subtitle, width)))
	}
	if page.tabs != "" {
		lines = append(lines, page.tabs)
	}
	if page.searchable {
		filter := m.menu.filter
		if filter == "" {
			filter = m.tr("type to search…", "pisz, aby wyszukać…")
		}
		lines = append(lines, m.palette.InputHint.Render(truncateVisible("/ "+filter, width)))
	}
	if height >= 14 {
		lines = append(lines, "")
	}
	split := width >= 86 && height >= 15
	tail := 1
	if !split && len(page.detail) > 0 && height >= 10 {
		tail = 3
	}
	if m.menu.formErr != "" {
		tail++
	}
	bodyHeight := maxInt(1, height-len(lines)-tail)
	listWidth := width
	if split {
		listWidth = width * 53 / 100
	}
	rowHeight := 1
	if len(page.items) > 0 && page.items[0].meta != "" && bodyHeight >= 6 {
		rowHeight = 2
	}
	count := maxInt(1, bodyHeight/rowHeight)
	start := maxInt(0, cursor-count/2)
	if start+count > len(page.items) {
		start = maxInt(0, len(page.items)-count)
	}
	end := minInt(len(page.items), start+count)
	body := make([]string, 0, bodyHeight)
	for i := start; i < end; i++ {
		item := page.items[i]
		prefix := "  "
		if i == cursor {
			prefix = "› "
		}
		badge := truncateVisible(item.badge, maxInt(0, listWidth/3))
		labelWidth := maxInt(1, listWidth-2)
		if badge != "" {
			labelWidth = maxInt(1, labelWidth-lipgloss.Width(badge)-1)
		}
		line := prefix + fitMenuLine(item.label, labelWidth)
		if badge != "" {
			line += " " + badge
		}
		line = fitMenuLine(line, listWidth)
		if i == cursor {
			line = m.palette.MenuSelected.Render(line)
		} else {
			line = m.palette.StatusValue.Render(line)
		}
		body = append(body, line)
		if rowHeight == 2 {
			body = append(body, m.palette.Dim.Render(fitMenuLine("  "+item.meta, listWidth)))
		}
	}
	if len(page.items) == 0 {
		empty := page.empty
		if empty == "" {
			empty = m.tr("No matches. Clear the search to try again.", "Brak wyników. Wyczyść wyszukiwanie i spróbuj ponownie.")
		}
		body = append(body, m.palette.Dim.Render(truncateVisible(empty, listWidth)))
	}
	for len(body) < bodyHeight {
		body = append(body, "")
	}
	if split {
		detailWidth := width - listWidth - 3
		detail := []string{m.palette.PanelTitle.Render(truncateVisible(page.detailTitle, detailWidth)), ""}
		for _, paragraph := range page.detail {
			detail = append(detail, strings.Split(ansi.Wrap(paragraph, detailWidth, ""), "\n")...)
		}
		for i := range body {
			right := ""
			if i < len(detail) {
				right = m.palette.Dim.Render(detail[i])
			}
			body[i] = fitMenuLine(body[i], listWidth) + m.palette.Rule.Render(" │ ") + truncateVisible(right, detailWidth)
		}
	}
	lines = append(lines, body...)
	if !split && len(page.detail) > 0 && height >= 10 {
		lines = append(lines, m.palette.Rule.Render(strings.Repeat("─", width)))
		lines = append(lines, m.palette.Dim.Render(truncateVisible(page.detail[0], width)))
	}
	if m.menu.formErr != "" {
		lines = append(lines, m.palette.Error.Render(truncateVisible(m.menu.formErr, width)))
	}
	footer := page.footer
	if (start > 0 || end < len(page.items)) && !m.menu.editing && m.menu.kind != menuProviderForm && m.menu.kind != menuGoalForm {
		footer += " · " + m.tr("PgUp/PgDn more", "PgUp/PgDn dalej")
	}
	lines = append(lines, m.palette.InputHint.Render(truncateVisible(footer, width)))
	return strings.Join(lines, "\n")
}
