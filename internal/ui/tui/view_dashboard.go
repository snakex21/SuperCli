package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"supercli/internal/storage/goal"
)

// DashboardSnapshot contains presentation data shared with the app runtime.
// Model/reasoning live in the header, worker activity in its own panel.
type DashboardSnapshot struct {
	Project, Directory, Limits, Account              string
	Orchestrator                                     bool
	Goal                                             goal.ProgressSnapshot
	SessionTokens, DailyTokens, SessionCap, DailyCap int64
}

type contextSnapshot struct {
	Used, Window, CompactAt int
	Cached, Evaluated       int
	HasCache                bool
	Requests                int
}

func (m Model) renderDashboard() string {
	if m.dashboardFn == nil {
		return ""
	}
	d := m.dashboardFn()
	width := max(1, m.width)
	sep := m.palette.StatusSep.Render(" · ")
	workspace := d.Project
	if workspace == "" {
		workspace = m.tr("Workspace", "Katalog roboczy")
	}
	head := m.palette.StatusKey.Render(workspace)
	if d.Directory != "" {
		head += sep + m.palette.StatusDim.Render(d.Directory)
	}
	if d.Orchestrator {
		head += sep + m.palette.HeaderMode.Render(m.tr("coordinator", "koordynator"))
	}
	lines := []string{truncateVisible(head, width)}
	if d.Goal.Title != "" {
		progress := ""
		if d.Goal.Total > 0 {
			progress = fmt.Sprintf("%d/%d", d.Goal.Done, d.Goal.Total)
			if width >= 72 {
				progress = m.dashboardBar(d.Goal.Done, d.Goal.Total, 6) + " " + progress
			}
		}
		switch d.Goal.Verification {
		case "passed":
			progress += m.tr(" verified", " sprawdzono")
		case "failed":
			progress += m.tr(" check failed", " błąd weryfikacji")
		default:
			if d.Goal.Total > 0 && d.Goal.Done == d.Goal.Total {
				progress += m.tr(" verify", " do weryfikacji")
			}
		}
		label := m.palette.StatusDim.Render(m.tr("Goal  ", "Cel  "))
		right := m.palette.Success.Render(strings.TrimSpace(progress))
		if d.Goal.Verification == "failed" {
			right = m.palette.Error.Render(strings.TrimSpace(progress))
		}
		space := max(0, width-lipgloss.Width(label)-lipgloss.Width(right)-2)
		title := truncateVisible(strings.Join(strings.Fields(d.Goal.Title), " "), space)
		row := label + m.palette.StatusValue.Render(title)
		if right != "" {
			row += strings.Repeat(" ", max(1, width-lipgloss.Width(row)-lipgloss.Width(right))) + right
		}
		lines = append(lines, truncateVisible(row, width))
	}
	c := m.runtimeContext
	usage := dashboardTokens(d.SessionTokens, d.SessionCap)
	day := dashboardTokens(d.DailyTokens, d.DailyCap)
	ctxValue, ctxHint := m.tr("waiting", "oczekiwanie"), m.tr("after the first turn", "po pierwszej turze")
	if c.Window > 0 {
		pct := c.Used * 100 / c.Window
		ctxValue = fmt.Sprintf("%d%%  %s/%s", pct, compactTokens(c.Used), compactTokens(c.Window))
		ctxHint = m.dashboardBar(c.Used, c.Window, 10) + fmt.Sprintf(m.tr("  compact at %d%%", "  skracanie przy %d%%"), c.CompactAt)
	}
	cacheValue, cacheHint := m.tr("not reported", "brak danych"), m.tr("cached input", "wejście z cache")
	if c.HasCache {
		cacheValue = fmt.Sprintf("%d%%", c.Cached*100/max(1, c.Cached+c.Evaluated))
		cacheHint = fmt.Sprintf(m.tr("%s reused · %s evaluated", "%s z cache · %s przeliczone"), compactTokens(c.Cached), compactTokens(c.Evaluated))
	}
	if c.Requests > 0 {
		cacheValue += fmt.Sprintf(m.tr(" · %d requests today", " · %d żądań dziś"), c.Requests)
	}
	if width >= 90 && m.height >= 22 {
		w := (width - 2) / 3
		cards := []string{
			m.dashboardCard(m.tr("TOKENS", "TOKENY"), usage+m.tr(" session", " sesja"), day+m.tr(" today", " dziś"), w),
			m.dashboardCard(m.tr("CONTEXT", "KONTEKST"), ctxValue, ctxHint, w),
			m.dashboardCard("CACHE", cacheValue, cacheHint, width-2*w-2),
		}
		lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, cards[0], " ", cards[1], " ", cards[2]))
	} else {
		lines = append(lines, truncateVisible(m.palette.StatusKey.Render(m.tr("Tokens ", "Tokeny "))+usage+sep+m.palette.StatusDim.Render(day+m.tr(" today", " dziś")), width))
		lines = append(lines, truncateVisible(m.palette.StatusKey.Render(m.tr("Context ", "Kontekst "))+ctxValue+sep+m.palette.StatusDim.Render("cache "+cacheValue), width))
	}
	if d.Limits != "" || d.Account != "" {
		lines = append(lines, truncateVisible(m.palette.StatusDim.Render(m.tr("Limits  ", "Limity  ")+strings.TrimSpace(d.Limits+" "+d.Account)), width))
	}
	return strings.Join(lines, "\n")
}

func dashboardTokens(used, cap int64) string {
	s := compactTokens(int(used))
	if cap > 0 {
		s += "/" + compactTokens(int(cap))
	}
	return s
}

func (m Model) dashboardBar(used, total, size int) string {
	filled := 0
	if total > 0 {
		filled = min(size, max(0, used*size/total))
	}
	style := m.palette.Success
	if total > 0 && used*100/total >= 85 {
		style = m.palette.HeaderMode
	}
	if total > 0 && used*100/total >= 95 {
		style = m.palette.Error
	}
	return style.Render(strings.Repeat("━", filled)) + m.palette.StatusSep.Render(strings.Repeat("─", size-filled))
}

func (m Model) dashboardCard(label, value, hint string, width int) string {
	inner := max(1, width-4)
	body := m.palette.StatusDim.Render(truncateVisible(label, inner)) + "\n" +
		m.palette.StatusValue.Bold(true).Render(truncateVisible(value, inner)) + "\n" +
		m.palette.StatusDim.Render(truncateVisible(hint, inner))
	return m.palette.Panel.Padding(0, 1).Width(width - 2).Render(body)
}

// Layout only needs row counts, not a second render of all dashboard cards.
func (m Model) dashboardHeight() int {
	if m.dashboardFn == nil {
		return 0
	}
	d := m.dashboardFn()
	rows := 3
	if m.width >= 90 && m.height >= 22 {
		rows = 6
	}
	if d.Goal.Title != "" {
		rows++
	}
	if d.Limits != "" || d.Account != "" {
		rows++
	}
	return rows
}
