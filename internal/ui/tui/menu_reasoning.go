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

func reasoningMenuOptions() []reasoningMenuOption { return reasoningMenuOptionsFor("en") }
func reasoningMenuOptionsFor(language string) []reasoningMenuOption {
	return []reasoningMenuOption{
		{Label: textFor(language, "tui.menu_reasoning.ecc40b6a46"), Value: "", Desc: textFor(language, "tui.reasoning.06a0e222ed")},
		{Label: textFor(language, "tui.reasoning.140bedbf9c"), Value: "none", Desc: textFor(language, "tui.reasoning.6040a9c4a9")},
		{Label: textFor(language, "tui.reasoning.a703788f83"), Value: "minimal", Desc: textFor(language, "tui.reasoning.2819358bdb")},
		{Label: textFor(language, "tui.reasoning.6c1ff09db3"), Value: "low", Desc: textFor(language, "tui.reasoning.8cce663ae4")},
		{Label: textFor(language, "tui.reasoning.c082456a77"), Value: "medium", Desc: textFor(language, "tui.reasoning.1c9ca5d08f")},
		{Label: textFor(language, "tui.reasoning.6ef7c9b15e"), Value: "high", Desc: textFor(language, "tui.reasoning.95b9990b05")},
		{Label: textFor(language, "tui.reasoning.b5255978be"), Value: "xhigh", Desc: textFor(language, "tui.reasoning.c596119f7c")},
		{Label: textFor(language, "tui.reasoning.9baf3a4031"), Value: "max", Desc: textFor(language, "tui.reasoning.bde6d6ae2b")},
	}
}
func (m Model) allLocalizedReasoningMenuOptions() []reasoningMenuOption {
	return reasoningMenuOptionsFor(m.language)
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
				opt.Label = m.tr("tui.menu_reasoning.ca7981b46e")
				opt.Desc = m.tr("tui.menu_reasoning.f19cfd4fe1")
			} else {
				opt.Label = m.tr("tui.menu_reasoning.1300117561")
				opt.Desc = m.tr("tui.menu_reasoning.e4d1d82495")
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
			return m.tr("tui.menu_reasoning.ca7981b46e")
		}
		if value != "" {
			return m.tr("tui.menu_reasoning.1300117561")
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
		m.setStatus(m.tr("tui.menu_reasoning.636c852945")+err.Error(), false)
	} else {
		state := llm.ProviderReasoningState(m.llm)
		label := m.reasoningLabel(state.Effective, state.ToggleOnly)
		if opt.Value == "" {
			label = m.tr("tui.menu_reasoning.ecc40b6a46")
		} else if label == "" {
			label = m.tr("tui.menu_reasoning.c980985b9e")
		}
		m.setStatus(m.tr("tui.menu_reasoning.d5c955f93b")+label, true)
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
		configured = m.tr("tui.menu_reasoning.ecc40b6a46")
	}
	if effective == "" {
		effective = m.tr("tui.menu_reasoning.8513a9ecd7")
	}
	page := menuPage{title: m.tr("tui.menu_reasoning.3236aeec43"),
		subtitle: m.reasoningModelName() + " · " + m.tr("tui.menu_reasoning.da2a43583d") + effective,
		footer:   m.tr("tui.menu_reasoning.5c07cbff09")}
	options := m.localizedReasoningMenuOptions()
	for _, opt := range options {
		badge := ""
		if opt.Value == state.Configured || (state.Configured != "" && state.Selected != "" && opt.Value == state.Selected) {
			badge = m.tr("tui.menu_models_render.a1922b55b9")
		}
		page.items = append(page.items, menuListItem{label: opt.Label, badge: badge})
	}
	if len(options) > 0 {
		opt := options[minInt(m.menu.cursor, len(options)-1)]
		page.detailTitle = opt.Label
		page.detail = []string{opt.Desc, "", m.tr("tui.menu_reasoning.6319fc4737") + configured, m.tr("tui.menu_reasoning.0d30db04bf") + effective}
	}
	if state.ToggleOnly {
		page.detail = append(page.detail, "", m.tr("tui.menu_reasoning.b063256851"))
	} else if state.Adjusted {
		page.detail = append(page.detail, "", m.tr("tui.menu_reasoning.c279ba6b80"))
	} else if !state.Supported {
		page.detail = append(page.detail, "", m.tr("tui.menu_reasoning.f954554920"))
	}
	return m.renderMenuPage(page)
}
