package tui

import (
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
		{Label: "provider default", Value: "", Desc: "let the provider choose its default"},
		{Label: "none", Value: "none", Desc: "explicitly disable when the provider supports a none value"},
		{Label: "minimal", Value: "minimal", Desc: "smallest thinking budget if accepted by the backend"},
		{Label: "low", Value: "low", Desc: "low thinking budget"},
		{Label: "medium", Value: "medium", Desc: "balanced thinking budget"},
		{Label: "high", Value: "high", Desc: "larger thinking budget"},
		{Label: "xhigh", Value: "xhigh", Desc: "maximum thinking budget where supported"},
	}
}

func (m Model) allLocalizedReasoningMenuOptions() []reasoningMenuOption {
	if m.language != "pl" {
		return reasoningMenuOptions()
	}
	return []reasoningMenuOption{
		{Label: "domyślne dostawcy", Value: "", Desc: "pozostaw wybór dostawcy"},
		{Label: "brak", Value: "none", Desc: "wyłącz jawnie, jeśli dostawca obsługuje tę wartość"},
		{Label: "minimalne", Value: "minimal", Desc: "najmniejszy budżet myślenia akceptowany przez backend"},
		{Label: "niskie", Value: "low", Desc: "niski budżet myślenia"},
		{Label: "średnie", Value: "medium", Desc: "zrównoważony budżet myślenia"},
		{Label: "wysokie", Value: "high", Desc: "większy budżet myślenia"},
		{Label: "maksymalne", Value: "xhigh", Desc: "największy obsługiwany budżet myślenia"},
	}
}

func (m Model) localizedReasoningMenuOptions() []reasoningMenuOption {
	state := llm.ProviderReasoningState(m.llm)
	all := m.allLocalizedReasoningMenuOptions()
	options := []reasoningMenuOption{all[0]}
	for _, opt := range all[1:] {
		if !containsString(state.Levels, opt.Value) {
			continue
		}
		if state.ToggleOnly {
			if opt.Value == "none" {
				opt.Label = m.tr("Off", "Wyłączone")
				opt.Desc = m.tr("disable thinking", "wyłącz myślenie")
			} else {
				opt.Label = m.tr("On", "Włączone")
				opt.Desc = m.tr("enable thinking", "włącz myślenie")
			}
		}
		options = append(options, opt)
	}
	return options
}

func (m Model) reasoningOptionIndex(value string) int {
	for i, opt := range m.localizedReasoningMenuOptions() {
		if opt.Value == value {
			return i
		}
	}
	return 0
}

func (m Model) reasoningLabel(value string, toggleOnly bool) string {
	if toggleOnly {
		if value == "none" {
			return m.tr("Off", "Wyłączone")
		}
		if value != "" {
			return m.tr("On", "Włączone")
		}
	}
	return value
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
	opts := m.localizedReasoningMenuOptions()
	if len(opts) == 0 {
		return m.closeMenu()
	}
	opt := opts[minInt(m.menu.cursor, len(opts)-1)]
	if err := llm.SetReasoningEffort(opt.Value); err != nil {
		m.setStatus(m.tr("Reasoning: ", "Myślenie: ")+err.Error(), false)
	} else {
		state := llm.ProviderReasoningState(m.llm)
		label := m.reasoningLabel(state.Effective, state.ToggleOnly)
		if opt.Value == "" {
			label = m.tr("provider default", "domyślne dostawcy")
		} else if label == "" {
			label = m.tr("provider default (parameter not sent)", "domyślne dostawcy (parametr niewysyłany)")
		}
		m.setStatus(m.tr("Thinking: ", "Myślenie: ")+label, true)
		m.persistReasoningEffort(opt.Value)
	}
	next, _ := m.backMenu()
	return next, m.statusClearCmd()
}

func (m Model) renderReasoningMenu() string {
	state := llm.ProviderReasoningState(m.llm)
	configured := m.reasoningLabel(state.Configured, state.ToggleOnly)
	effective := m.reasoningLabel(state.Effective, state.ToggleOnly)
	if configured == "" {
		configured = m.tr("provider default", "domyślne dostawcy")
	}
	if effective == "" {
		effective = m.tr("not sent", "niewysyłane")
	}
	page := menuPage{title: m.tr("Reasoning effort", "Poziom myślenia"),
		subtitle: m.reasoningModelName() + " · " + m.tr("effective: ", "aktywne: ") + effective,
		footer:   m.tr("↑↓ choose · Enter apply", "↑↓ wybierz · Enter zastosuj")}
	options := m.localizedReasoningMenuOptions()
	for _, opt := range options {
		badge := ""
		if opt.Value == state.Configured || (state.ToggleOnly && opt.Value == state.Effective && state.Configured != "") {
			badge = m.tr("● active", "● aktywne")
		}
		page.items = append(page.items, menuListItem{label: opt.Label, badge: badge})
	}
	if len(options) > 0 {
		opt := options[minInt(m.menu.cursor, len(options)-1)]
		page.detailTitle = opt.Label
		page.detail = []string{opt.Desc, "", m.tr("Configured: ", "Ustawione: ") + configured, m.tr("Effective: ", "Efektywne: ") + effective}
	}
	if state.ToggleOnly {
		page.detail = append(page.detail, "", m.tr("This model supports on/off only.", "Ten model obsługuje tylko włączanie i wyłączanie myślenia."))
	} else if state.Adjusted {
		page.detail = append(page.detail, "", m.tr("Adjusted to levels accepted by this provider.", "Dopasowano do poziomów obsługiwanych przez dostawcę."))
	} else if !state.Supported {
		page.detail = append(page.detail, "", m.tr("This model does not advertise reasoning controls.", "Ten model nie zgłasza obsługi zmiany myślenia."))
	}
	return m.renderMenuPage(page)
}
