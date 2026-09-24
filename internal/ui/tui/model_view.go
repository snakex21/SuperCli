// Package tui is the Bubble Tea presentation layer. F25 replaces
// the raw transcript with a structured chat view (role-based
// colors), adds a status bar, inline event markers, a tool-
// name spinner, Ctrl+C run cancellation, and Wheel/PgUp/PgDn scrolling.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"supercli/internal/agent"
	"supercli/internal/llm"
)

func (m Model) View() string {
	if m.quitting {
		return "SuperCli closed.\n"
	}
	if m.mode == modeAsking && m.pendingAsk != nil {
		return renderAskView(m.pendingAsk, m.width, m.height, m.language)
	}
	if m.mode == modeDoctor && m.doctorReport != nil {
		return m.renderDoctorView()
	}
	if m.mode == modeMenu {
		return m.renderMenuView()
	}
	var b strings.Builder

	// 1. Header chrome
	b.WriteString(m.renderHeader())
	b.WriteString("\n")

	// 2. Chat area (scrollable)
	b.WriteString(m.viewport.View())

	if lines := m.workerPanelLines(); len(lines) > 0 {
		b.WriteString("\n" + strings.Join(lines, "\n"))
	}

	// 3. Separator line
	sep := m.rule()
	b.WriteString("\n")
	b.WriteString(sep)
	b.WriteString("\n")

	// 4. Status bar
	if m.busy {
		fmt.Fprintf(&b, "%s %s\n", m.spinner.View(), m.palette.InputHint.Render(m.tr("working · Ctrl+C interrupt · Esc cancel", "praca · Ctrl+C przerwij · Esc anuluj")))
	}
	if m.dashboardFn != nil {
		b.WriteString(m.renderDashboard() + "\n")
	} else if m.statusFn != nil {
		if line := m.statusFn(); line != "" {
			fmt.Fprintf(&b, "%s\n", line)
		}
	}
	if m.dashboardFn == nil && m.runtimeHUD != "" {
		fmt.Fprintf(&b, "%s\n", m.palette.InputHint.Render(truncateVisible(m.runtimeHUD, m.width)))
	}

	// 5. Autocomplete popup (above input line)
	acView := renderAutocomplete(&m.autocomp, m.width, m.palette, m.language)
	if acView != "" {
		b.WriteString(acView)
		b.WriteString("\n")
	}

	if len(m.pendingAttachments) > 0 {
		b.WriteString(m.palette.InputHint.Render(truncateVisible(attachmentDisplay(m.pendingAttachments)+m.tr(" · Ctrl+O add · Ctrl+K → Attachments: remove", " · Ctrl+O dodaj · Ctrl+K → Załączniki: usuń"), m.width)) + "\n")
	}
	// 6. Input box + persistent key hints
	b.WriteString(m.renderInputBox())
	b.WriteString("\n")
	b.WriteString(m.renderHintLine())
	return b.String()
}

// renderHeader draws the slim top bar:
//
//	✻ SuperCli 0.6.0 · <model>                    <mode>
func (m Model) renderHeader() string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	mode := m.tr("● ready", "● gotowy")
	if m.busy {
		mode = m.tr("● working", "● pracuje")
	} else if m.mode == modeAsking {
		mode = m.tr("● asking", "● pyta")
	}
	if m.planMode {
		mode = "PLAN · " + mode
	}
	model := m.tr("no-model", "brak modelu")
	if m.llm != nil {
		model = m.llm.Name()
		// Show the active reasoning-effort level next to the
		// model name, but only when the model actually supports
		// the parameter and a level is set ("" = provider
		// default → show nothing). Read at render time, so it
		// refreshes immediately after /reasoning, Ctrl+R, or a
		// model switch.
		if state := llm.ProviderReasoningState(m.llm); state.Effective != "" {
			model += " (" + m.reasoningLabel(state.Effective, state.ToggleOnly) + ")"
		}
	}
	name := "> SuperCli"
	if m.version != "" {
		name += " " + m.version
	}
	sep := m.palette.StatusSep.Render(" · ")
	left := m.palette.Header.Render(name) + sep + m.palette.HeaderDim.Render(model)
	right := m.palette.Success.Render(mode)
	if m.busy || m.mode == modeAsking || m.planMode {
		right = m.palette.HeaderMode.Render(mode)
	}
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	line := left + strings.Repeat(" ", gap) + right
	if lipgloss.Width(line) > width {
		return truncateVisible(line, width)
	}
	return line
}

// renderInputBox wraps the textarea in a rounded border that
// uses the accent color while focused and a faint border when
// blurred. Visual only — keybindings are unchanged.
func (m Model) renderInputBox() string {
	style := m.palette.InputBorderBlurred
	if m.input.Focused() {
		style = m.palette.InputBorderFocused
	}
	if m.width > 4 {
		style = style.Width(m.width - 4)
	}
	return style.Render(m.input.View())
}

func (m Model) rule() string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	return m.palette.Rule.Render(strings.Repeat("─", width))
}

func (m Model) renderHintLine() string {
	if m.statusOverride != "" {
		return m.renderNotice(m.menuWidth())
	}
	if !m.viewport.AtBottom() {
		return m.palette.HeaderMode.Render(truncateVisible(m.tr("Reading history · End: follow latest · Wheel/PgUp/PgDn scroll", "Czytasz historię · End: śledź odpowiedź · Kółko/PgUp/PgDn przewiń"), m.width))
	}
	hints := []string{
		m.tr("While working: type + Enter queues", "Podczas pracy: wpisz + Enter dodaje do kolejki"),
		m.tr("Tab actions", "Tab działania"), m.tr("Enter send", "Enter wyślij"), m.tr("Ctrl+O files", "Ctrl+O pliki"),
		m.tr("Alt+Enter newline", "Alt+Enter nowa linia"), m.tr("Ctrl+Y copy reply", "Ctrl+Y kopiuj odpowiedź"),
		m.tr("Ctrl+R reasoning menu", "Ctrl+R poziom myślenia"), m.tr("Esc clear", "Esc wyczyść"),
		m.tr("Ctrl+C interrupt", "Ctrl+C przerwij"), m.tr("Wheel/PgUp/PgDn scroll", "Kółko/PgUp/PgDn przewiń"),
		m.tr("Shift+T thinking", "Shift+T myślenie"), m.tr("Shift+E expand", "Shift+E rozwiń"),
		m.tr("/ advanced", "/ zaawansowane"),
	}
	line := strings.Join(hints, " · ")
	if m.width > 0 && lipgloss.Width(line) > m.width {
		line = m.tr("Tab actions · Enter send · Ctrl+O files · Alt+Enter newline · Esc clear · Ctrl+C interrupt", "Tab działania · Enter wyślij · Ctrl+O pliki · Alt+Enter nowa linia · Esc wyczyść · Ctrl+C przerwij")
	}
	if m.width > 0 && lipgloss.Width(line) > m.width {
		line = m.tr("Tab actions · Enter send · Ctrl+O files · Ctrl+C stop", "Tab działania · Enter wyślij · Ctrl+O pliki · Ctrl+C stop")
	}
	if m.width > 0 {
		line = truncateVisible(line, m.width)
	}
	return m.palette.InputHint.Render(line)
}

func truncateVisible(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(s, width, "…")
}

func (m Model) viewportHeight() int {
	if m.height <= 0 {
		return 20
	}
	// Header + separator + input (+2 border rows) + key-hints
	// + at least one status line.
	reserved := 8 + len(m.workerPanelLines())
	if len(m.pendingAttachments) > 0 {
		reserved++
	}
	// Multi-line input box: account for the extra rows.
	if ih := m.input.Height(); ih > 1 {
		reserved += ih - 1
	}
	reserved += m.autocompleteHeight()
	if m.busy {
		reserved++
	}
	if m.dashboardFn != nil {
		reserved += m.dashboardHeight()
	} else if m.statusFn != nil {
		if s := m.statusFn(); s != "" {
			reserved += visualLineCount(s, m.width)
		}
	}
	if m.dashboardFn == nil && m.runtimeHUD != "" {
		reserved++
	}
	h := m.height - reserved
	if h < 3 {
		return 3
	}
	return h
}

type contextReporter interface {
	ContextReport() agent.ContextReport
}

type turnBreakdownReporter interface {
	LastTurnBreakdown() (cached, evaluated, generated int, ok bool)
}

// refreshRuntimeHUD pays the O(history) context estimate once per completed
// turn, never per frame. The HUD is presentation-only and causes no model call.
func (m *Model) refreshRuntimeHUD() {
	reporter, ok := m.agent.(contextReporter)
	if !ok {
		return
	}
	report := reporter.ContextReport()
	used := report.RequestTokens
	if used <= 0 {
		used = report.EstimatedTokens + report.ToolSchemaTokens + report.CatalogTokens
	}
	m.runtimeContext = contextSnapshot{Used: used, Window: report.Window}
	parts := make([]string, 0, 3)
	if report.Window > 0 {
		pct := used * 100 / report.Window
		parts = append(parts, fmt.Sprintf("ctx %d%% (%s/%s)", pct, compactTokens(used), compactTokens(report.Window)))
		threshold := report.CompactThreshold
		if threshold <= 0 {
			threshold = agent.AutoCompactThreshold(report.Window)
		}
		m.runtimeContext.CompactAt = (threshold*100 + report.Window/2) / report.Window
		parts = append(parts, fmt.Sprintf("compact %d%%", m.runtimeContext.CompactAt))
	}
	if breakdown, ok := m.agent.(turnBreakdownReporter); ok {
		cached, evaluated, _, set := breakdown.LastTurnBreakdown()
		if set && cached+evaluated > 0 {
			m.runtimeContext.Cached, m.runtimeContext.Evaluated = cached, evaluated
			m.runtimeContext.HasCache = true
			parts = append(parts, fmt.Sprintf("cache %d%%", cached*100/(cached+evaluated)))
		}
	}
	// Daily request count for the active endpoint (e.g. the OpenCode
	// Zen free tier's ~100/day). Keyed by endpoint, never by API key,
	// so keyless providers report exactly the same way.
	if baseURL := m.activeProviderBaseURL(); baseURL != "" {
		if n := llm.ProviderRequestsTodayFlexible(baseURL); n > 0 {
			m.runtimeContext.Requests = n
			parts = append(parts, fmt.Sprintf("%d req today", n))
		}
	}
	m.runtimeHUD = strings.Join(parts, " · ")
	m.resizeViewport()
}

// activeProviderBaseURL resolves the configured base URL of the
// provider the session currently talks to. Empty when the manager is
// unavailable or the provider is unknown — the HUD simply omits the
// quota segment then.
func (m *Model) activeProviderBaseURL() string {
	if m.providerMgr == nil || m.activeProvider == "" {
		return ""
	}
	for _, p := range m.providerMgr.Configured() {
		if p.Name == m.activeProvider {
			return p.BaseURL
		}
	}
	return ""
}

func compactTokens(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fm", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

// visualLineCount returns the number of terminal rows occupied by text after
// wrapping at width. It deliberately uses lipgloss.Width so ANSI styling does
// not make the height calculation drift from what Bubble Tea renders.
func visualLineCount(s string, width int) int {
	if s == "" {
		return 0
	}
	if width <= 0 {
		return len(strings.Split(s, "\n"))
	}
	total := 0
	for _, line := range strings.Split(s, "\n") {
		w := lipgloss.Width(line)
		rows := (w + width - 1) / width
		if rows < 1 {
			rows = 1
		}
		total += rows
	}
	return total
}

func (m Model) autocompleteHeight() int {
	if m.autocomp.kind == autocompNone {
		return 0
	}
	filtered := filterItems(m.autocomp.items, m.autocomp.query)
	if len(filtered) == 0 {
		return 0
	}
	visible := len(filtered)
	if visible > autocompMaxVisible {
		visible = autocompMaxVisible
	}
	// renderAutocomplete uses:
	// title + visible rows + optional detail separator/detail + footer separator/footer + bottom.
	height := 1 + visible + 2 + 1
	selected := filtered[minInt(m.autocomp.cursor, len(filtered)-1)]
	if selected.Desc != "" || selected.Hint != "" {
		height += 2
	}
	return height
}

func welcome(opts Options, p Palette) string {
	return welcomeAtWidth(opts, p, 80)
}

func welcomeAtWidth(opts Options, p Palette, width int) string {
	return welcomeAtSize(opts, p, width, 0)
}

func welcomeAtSize(opts Options, p Palette, width, height int) string {
	language := normalizeLanguage(opts.Language)
	tr := func(en, pl string) string { return textFor(language, en, pl) }
	model := tr("not configured", "nieskonfigurowany")
	if opts.LLM != nil {
		model = opts.LLM.Name()
	}
	if width <= 0 {
		width = 80
	}
	// A short terminal needs the chat viewport more than decorative cards.
	// Keep the same actions visible in a five-line, borderless empty state.
	if height > 0 && height < 28 {
		modelWidth := width - lipgloss.Width("model ")
		if modelWidth < 8 {
			modelWidth = 8
		}
		lines := []string{
			p.PanelTitle.Render("> SuperCli") + " " + p.PanelMuted.Render(tr("· portable AI coding agent", "· przenośny agent programistyczny AI")),
			p.Bold.Render(tr("Welcome back", "Witaj ponownie")) + " " + p.Dim.Render(tr("· ask for a change, inspect files, or run a plan", "· zleć zmianę, sprawdź pliki lub uruchom plan")),
			p.Dim.Render(tr("model ", "model ")) + p.StatusValue.Render(truncateVisible(model, modelWidth)),
			p.HeaderMode.Render("Tab") + p.Dim.Render(tr(" actions · ", " działania · ")) + p.HeaderMode.Render("@") + p.Dim.Render(tr(" attach file · ", " dołącz plik · ")) + p.HeaderMode.Render("/") + p.Dim.Render(tr(" advanced", " zaawansowane")),
		}
		for i := range lines {
			lines[i] = truncateVisible(lines[i], width)
		}
		return strings.Join(lines, "\n") + "\n"
	}
	cardWidth := (width - 7) / 2
	if cardWidth < 32 {
		cardWidth = 32
	}
	leftContent :=
		p.PanelTitle.Render("> SuperCli") + "\n" +
			p.PanelMuted.Render(tr("portable AI coding agent", "przenośny agent programistyczny AI")) + "\n\n" +
			p.Bold.Render(tr("Welcome back", "Witaj ponownie")) + "\n" +
			tr("Ask for a change, inspect files, or run a plan.", "Zleć zmianę, sprawdź pliki lub uruchom plan.") + "\n\n" +
			p.Dim.Render("model ") + p.StatusValue.Render(model)
	rightContent :=
		p.PanelTitle.Render(tr("Start here", "Zacznij tutaj")) + "\n\n" +
			p.HeaderMode.Render("Tab") + p.Dim.Render(tr("        action centre", "        centrum działań")) + "\n" +
			p.HeaderMode.Render("Enter") + p.Dim.Render(tr("      send message", "      wyślij wiadomość")) + "\n" +
			p.HeaderMode.Render("@") + p.Dim.Render(tr("          attach a project file", "          dołącz plik projektu")) + "\n" +
			p.HeaderMode.Render("/") + p.Dim.Render(tr("          advanced commands", "          komendy zaawansowane")) + "\n\n" +
			p.Dim.Render(tr("Esc clears · Ctrl+C interrupts · Shift+E expands tools", "Esc czyści · Ctrl+C przerywa · Shift+E rozwija narzędzia"))
	left := p.Panel.Width(cardWidth).Render(leftContent)
	right := p.Panel.Width(cardWidth).Render(rightContent)
	horizontal := lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
	if lipgloss.Width(horizontal) <= width {
		return horizontal + "\n"
	}
	// Narrow terminals get the same cards stacked. Subtract the border
	// width so no line is clipped by the terminal's final column.
	stackWidth := width - 2
	if stackWidth < 32 {
		stackWidth = 32
	}
	left = p.Panel.Width(stackWidth).Render(leftContent)
	right = p.Panel.Width(stackWidth).Render(rightContent)
	return lipgloss.JoinVertical(lipgloss.Left, left, right) + "\n"
}
