package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/llm"
)

type reasoningMenuOption struct {
	Label string
	Value string
	Desc  string
}

func reasoningMenuOptions() []reasoningMenuOption {
	return []reasoningMenuOption{
		{Label: "off / provider default", Value: "", Desc: "do not send a reasoning/thinking budget"},
		{Label: "none", Value: "none", Desc: "explicitly disable when the provider supports a none value"},
		{Label: "minimal", Value: "minimal", Desc: "smallest thinking budget if accepted by the backend"},
		{Label: "low", Value: "low", Desc: "low thinking budget"},
		{Label: "medium", Value: "medium", Desc: "balanced thinking budget"},
		{Label: "high", Value: "high", Desc: "larger thinking budget"},
		{Label: "xhigh", Value: "xhigh", Desc: "maximum thinking budget where supported"},
	}
}

func (m Model) localizedReasoningMenuOptions() []reasoningMenuOption {
	if m.language != "pl" {
		return reasoningMenuOptions()
	}
	return []reasoningMenuOption{
		{Label: "wyłączone / domyślne dostawcy", Value: "", Desc: "nie wysyłaj budżetu myślenia"},
		{Label: "brak", Value: "none", Desc: "wyłącz jawnie, jeśli dostawca obsługuje tę wartość"},
		{Label: "minimalne", Value: "minimal", Desc: "najmniejszy budżet myślenia akceptowany przez backend"},
		{Label: "niskie", Value: "low", Desc: "niski budżet myślenia"},
		{Label: "średnie", Value: "medium", Desc: "zrównoważony budżet myślenia"},
		{Label: "wysokie", Value: "high", Desc: "większy budżet myślenia"},
		{Label: "maksymalne", Value: "xhigh", Desc: "największy obsługiwany budżet myślenia"},
	}
}

func reasoningOptionIndex(value string) int {
	for i, opt := range reasoningMenuOptions() {
		if opt.Value == value {
			return i
		}
	}
	return 0
}

func (m Model) reasoningModelName() string {
	if m.modelSwapper != nil && m.modelSwapper.CurrentModel() != "" {
		return m.modelSwapper.CurrentModel()
	}
	if m.llm != nil {
		return m.llm.Name()
	}
	return "no-model"
}

func (m Model) selectReasoningEffort() (tea.Model, tea.Cmd) {
	opts := reasoningMenuOptions()
	if len(opts) == 0 {
		return m.closeMenu()
	}
	opt := opts[minInt(m.menu.cursor, len(opts)-1)]
	if err := llm.SetReasoningEffort(opt.Value); err != nil {
		m.statusOverride = fmt.Sprintf("reasoning: %v", err)
	} else {
		if opt.Value == "" {
			m.statusOverride = "reasoning: off (provider default)"
		} else {
			model := m.reasoningModelName()
			_, effective, adjusted := llm.ReasoningEffortAdjustment(model)
			if adjusted {
				m.statusOverride = fmt.Sprintf("reasoning: %s (effective %s for %s)", opt.Value, effective, model)
			} else {
				m.statusOverride = "reasoning: " + opt.Value
			}
		}
		m.persistReasoningEffort(opt.Value)
	}
	m.mode = modeNormal
	m.menu = interactiveMenu{}
	m.input.Focus()
	return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return statusOverrideClearMsg{} })
}

func (m Model) renderReasoningMenu() string {
	width := m.menuWidth()
	model := m.reasoningModelName()
	configured, effective, adjusted := llm.ReasoningEffortAdjustment(model)
	if configured == "" {
		configured = m.tr("off / provider default", "wyłączone / domyślne dostawcy")
	}
	if effective == "" {
		effective = m.tr("not sent", "niewysyłane")
	}
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render(m.tr("Reasoning effort", "Poziom myślenia")) + "\n\n")
	b.WriteString(truncateText(fmt.Sprintf("model:      %s", model), width) + "\n")
	b.WriteString(truncateVisible(fmt.Sprintf(m.tr("configured: %s", "ustawione:   %s"), configured), width) + "\n")
	if adjusted {
		b.WriteString(truncateVisible(fmt.Sprintf(m.tr("effective:  %s (adjusted from backend evidence)", "efektywne:   %s (dopasowane na podstawie backendu)"), effective), width) + "\n")
	} else {
		b.WriteString(truncateVisible(fmt.Sprintf(m.tr("effective:  %s", "efektywne:   %s"), effective), width) + "\n")
	}
	if supported, ok := llm.SupportedReasoningEfforts(model); ok {
		b.WriteString(truncateVisible("backend:    "+strings.Join(supported, " | "), width) + "\n")
	} else if llm.SupportsReasoningEffort(model) {
		b.WriteString(truncateVisible(m.tr("backend:    unknown yet — will learn from API errors", "backend:    jeszcze nieznany — zostanie rozpoznany z błędów API"), width) + "\n")
	} else {
		b.WriteString(truncateVisible(m.tr("backend:    model family does not advertise reasoning effort", "backend:    rodzina modelu nie zgłasza obsługi poziomu myślenia"), width) + "\n")
	}
	b.WriteString("\n")
	opts := m.localizedReasoningMenuOptions()
	supported, learned := llm.SupportedReasoningEfforts(model)
	for i, opt := range opts {
		prefix := "  "
		if i == m.menu.cursor {
			prefix = "> "
		}
		label := opt.Label
		if opt.Value != "" && learned && !containsString(supported, opt.Value) {
			label += m.tr(" (not in learned backend list)", " (brak na wykrytej liście backendu)")
		}
		plain := truncateText(fmt.Sprintf("%-34s %s", label, opt.Desc), width-2)
		line := plain
		if i == m.menu.cursor {
			line = m.palette.HeaderMode.Render(prefix + line)
		} else {
			line = prefix + m.palette.Dim.Render(line)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + m.palette.InputHint.Render(truncateText(m.tr("↑↓ move · Enter apply · Esc back", "↑↓ wybierz · Enter zastosuj · Esc wróć"), width)))
	return b.String()
}
