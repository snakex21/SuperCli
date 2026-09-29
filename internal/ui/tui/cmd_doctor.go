package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"supercli/internal/buildinfo"
	"supercli/internal/system/doctor"
)

func (m Model) renderDoctorView() string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	var b strings.Builder
	b.WriteString(m.renderHeader())
	b.WriteString("\n")
	b.WriteString(renderDoctorReport(*m.doctorReport, m.palette, width, m.language))
	b.WriteString("\n")
	b.WriteString(m.palette.InputHint.Render(m.tr("tui.doctor.15f658f25b")))
	return b.String()
}

func (m Model) handleDoctorKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter", "esc", "q":
		m.mode = modeNormal
		m.doctorReport = nil
		return m, nil
	case "r", "R":
		rep := doctor.Run(nil_to_ctx(), doctor.Env{
			Version:     buildinfo.Version,
			Home:        m.home,
			DataDir:     m.dataDir,
			Provider:    m.llm,
			Registry:    m.toolRegistry,
			Sessions:    m.sessionStore,
			ProviderMgr: m.providerMgr,
			Caps:        m.caps,
		})
		m.doctorReport = &rep
		return m, nil
	}
	return m, nil
}

func renderDoctorReport(rep doctor.Report, p Palette, width int, languages ...string) string {
	language := "en"
	if len(languages) > 0 {
		language = languages[0]
	}
	tr := func(key string) string { return textFor(language, key) }
	if width <= 0 {
		width = 80
	}
	boxWidth := width
	if boxWidth > 88 {
		boxWidth = 88
	}
	if boxWidth < 54 {
		boxWidth = 54
	}
	inner := boxWidth - 2
	ok, warn, fail, skip := rep.Summary()
	var b strings.Builder
	title := fmt.Sprintf(tr("tui.doctor.1fbd060343"), rep.Version)
	b.WriteString(p.Rule.Render("╭─ "))
	b.WriteString(p.PanelTitle.Render(title))
	b.WriteString(p.Rule.Render(strings.Repeat("─", maxInt(0, boxWidth-len([]rune(title))-4)) + "╮"))
	b.WriteByte('\n')
	summary := fmt.Sprintf(tr("tui.doctor.67af8e319f"), ok, warn, fail, skip)
	b.WriteString(p.Rule.Render("│"))
	b.WriteString(p.InputHint.Render(padRight(truncateText(summary, inner), inner)))
	b.WriteString(p.Rule.Render("│\n"))
	b.WriteString(p.Rule.Render("├" + strings.Repeat("─", inner) + "┤\n"))
	for _, c := range rep.Checks {
		mark := doctorMark(c.Status, p)
		name := padRight(truncateText(c.Name, 16), 16)
		detailWidth := inner - 2 - 2 - 16 - 1
		if detailWidth < 12 {
			detailWidth = 12
		}
		detail := truncateMiddle(c.Detail, detailWidth)
		line := "  " + mark + " " + p.Bold.Render(name) + " " + p.StatusValue.Render(detail)
		b.WriteString(p.Rule.Render("│"))
		b.WriteString(line)
		if pad := inner - lipgloss.Width(line); pad > 0 {
			b.WriteString(strings.Repeat(" ", pad))
		}
		b.WriteString(p.Rule.Render("│\n"))
		if c.Remediation != "" {
			fix := tr("tui.doctor.c62e8e8bf9") + truncateMiddle(c.Remediation, inner-10)
			b.WriteString(p.Rule.Render("│"))
			fixLine := p.InputHint.Render(padRight(truncateText(fix, inner), inner))
			b.WriteString(fixLine)
			b.WriteString(p.Rule.Render("│\n"))
		}
	}
	b.WriteString(p.Rule.Render("├" + strings.Repeat("─", inner) + "┤\n"))
	footer := tr("tui.doctor.65705f0bdc")
	b.WriteString(p.Rule.Render("│"))
	b.WriteString(p.InputHint.Render(padRight(truncateText(footer, inner), inner)))
	b.WriteString(p.Rule.Render("│\n"))
	b.WriteString(p.Rule.Render("╰" + strings.Repeat("─", inner) + "╯"))
	return b.String()
}

func doctorMark(s doctor.Status, p Palette) string {
	switch s {
	case doctor.OK:
		return p.Success.Render("✓")
	case doctor.Warn:
		return p.HeaderMode.Render("⚠")
	case doctor.Fail:
		return p.Error.Render("✗")
	case doctor.Skip:
		return p.Dim.Render("–")
	default:
		return p.Dim.Render("?")
	}
}

func plainStatusMark(s doctor.Status) string {
	switch s {
	case doctor.OK:
		return "✓"
	case doctor.Warn:
		return "⚠"
	case doctor.Fail:
		return "✗"
	case doctor.Skip:
		return "–"
	default:
		return "?"
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func truncateMiddle(s string, width int) string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return s
	}
	if width <= 3 {
		return truncateText(s, width)
	}
	r := []rune(s)
	leftWidth := (width - 1) / 2
	rightWidth := width - 1 - leftWidth
	left := ""
	for i := 0; i < len(r); i++ {
		candidate := string(r[:i+1])
		if lipgloss.Width(candidate) > leftWidth {
			break
		}
		left = candidate
	}
	right := ""
	for i := len(r) - 1; i >= 0; i-- {
		candidate := string(r[i:])
		if lipgloss.Width(candidate) > rightWidth {
			break
		}
		right = candidate
	}
	out := left + "…" + right
	for lipgloss.Width(out) > width && len([]rune(right)) > 0 {
		rr := []rune(right)
		right = string(rr[1:])
		out = left + "…" + right
	}
	return out
}
