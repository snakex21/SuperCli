package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m onboardModel) View() string {
	p := DefaultPalette()
	var b strings.Builder
	header := p.PanelTitle.Render("✻ SuperCli") + p.PanelMuted.Render(m.tr("tui.onboard_view.59570f1941"))
	if m.width > 0 {
		header = truncateVisible(header, m.width)
	}
	b.WriteString(header + "\n")
	switch m.step {
	case onboardDetect:
		b.WriteString(p.PanelMuted.Render(m.tr("tui.onboard_view.09e85e2c68")) + "\n")
	case onboardMenu:
		return b.String() + m.renderProviderChoices()
	case onboardAuthMethod:
		b.WriteString("\n" + m.tr("tui.onboard_view.dd924e808c") + "\n\n")
		opts := []string{
			m.tr("tui.menu_providers_render.eaec73252e"),
			m.tr("tui.menu_providers_render.9c1a934db0"),
		}
		for i, o := range opts {
			line := fmt.Sprintf("%d. %s", i+1, o)
			if i == m.cursor {
				fmt.Fprintf(&b, "%s\n", p.Header.Render("> "+line))
			} else {
				fmt.Fprintf(&b, "%s\n", p.Dim.Render("  "+line))
			}
		}
		b.WriteString("\n" + p.InputHint.Render(m.tr("tui.onboard_view.ded543f5b1")) + "\n")
	case onboardURL:
		b.WriteString("\n" + m.tr("tui.onboard_view.4815f25033") + "\n")
		fmt.Fprintf(&b, "%s %s_\n", p.InputPrompt.Render(">"), m.input)
		b.WriteString("\n" + p.InputHint.Render(m.tr("tui.onboard_view.d604cf98d9")) + "\n")
	case onboardKey:
		b.WriteString("\n" + p.PanelTitle.Render(m.result.Name) + p.PanelMuted.Render(" · "+m.result.BaseURL) + "\n")
		masked := strings.Repeat("*", len([]rune(m.input)))
		b.WriteString("\n" + m.tr("tui.onboard_view.4ed6b45b27") + "\n")
		fmt.Fprintf(&b, "%s %s_\n", p.InputPrompt.Render(">"), masked)
		b.WriteString("\n" + p.InputHint.Render(m.tr("tui.onboard_view.d604cf98d9")) + "\n")
	case onboardLoadModels:
		b.WriteString(p.PanelMuted.Render("\n"+m.tr("tui.onboard_view.25c820a8be")+m.result.BaseURL+"...") + "\n")
	case onboardModels:
		b.WriteString(p.PanelMuted.Render(m.tr("tui.onboard_view.ab8f3519ba")+m.result.Name+"):") + "\n\n")
		// Show a window of up to 10 models around the cursor.
		start := 0
		if m.cursor > 9 {
			start = m.cursor - 9
		}
		end := minInt(start+10, len(m.models))
		for i := start; i < end; i++ {
			if i == m.cursor {
				fmt.Fprintf(&b, "%s\n", p.Header.Render("> "+m.models[i]))
			} else {
				fmt.Fprintf(&b, "%s\n", p.Dim.Render("  "+m.models[i]))
			}
		}
		if end < len(m.models) {
			fmt.Fprintf(&b, "%s\n", p.Dim.Render(fmt.Sprintf(m.tr("tui.onboard_view.7d0103f3b6"), len(m.models)-end)))
		}
		b.WriteString("\n" + p.InputHint.Render(m.tr("tui.onboard_view.ded543f5b1")) + "\n")
	case onboardVerify:
		b.WriteString(p.PanelMuted.Render("\n"+m.tr("tui.onboard_view.d98c0fdf2a")) + "\n")
	case onboardDone:
		b.WriteString(p.Success.Render(m.tr("tui.onboard_view.14db0f4517")) + "\n")
	}
	return b.String()
}

// RunOnboarding runs the wizard in its own bubbletea program
// and returns the user's choice. A TTY error or abort returns
// Skipped=true so the caller falls back to echo mode.
func RunOnboarding(language string, dataDirs ...string) OnboardResult {
	initial := onboardModel{language: normalizeLanguage(language)}
	if len(dataDirs) > 0 {
		initial.dataDir = dataDirs[0]
	}
	p := NewProgram(initial, tea.WithMouseCellMotion())
	final, err := p.Run()
	if err != nil {
		return OnboardResult{Skipped: true}
	}
	m, ok := final.(onboardModel)
	if !ok || m.aborted || m.step != onboardDone {
		return OnboardResult{Skipped: true}
	}
	return m.result
}

func (m onboardModel) renderProviderChoices() string {
	width, height := m.width, m.height
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 28
	}
	if width < 24 || height < 8 {
		return truncateVisible(m.tr("tui.onboard_view.6fd3e6609a"), width)
	}
	presentation := Model{
		language: m.language, palette: DefaultPalette(),
		width: minInt(width, 120), height: height - 1,
		menu: interactiveMenu{kind: menuProviderPredefined, cursor: m.cursor, filter: m.filter, formErr: m.errMsg},
	}
	page := menuPage{
		title:      m.tr("tui.menu_providers_render.e1d36c3ade"),
		subtitle:   m.tr("tui.onboard_view.188a6da089"),
		searchable: true,
		footer:     m.tr("tui.onboard_view.fc64b84595") + " · Tab / Shift+Tab",
	}
	for i, row := range m.filteredChoices() {
		page.items = append(page.items, menuListItem{label: row.label, meta: row.desc})
		if i == m.cursor {
			page.detailTitle = row.label
			page.detail = []string{row.desc, "", row.provider.BaseURL}
			if row.provider.Type != "" {
				page.detail = append(page.detail, "", presentation.providerProtocolLabel(row.provider.Type))
			}
		}
	}
	return presentation.renderMenuPage(page)
}
